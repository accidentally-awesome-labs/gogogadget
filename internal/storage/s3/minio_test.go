package s3

// The protocol-container tier for this adapter: the S3 client this package
// ships, run against a real S3 implementation.
//
// contract_test.go beside this file runs the SAME contract table against
// deterministicS3Backend, an in-process fake written in this package. That
// fake can only confirm the adapter agrees with this package's own beliefs
// about the S3 wire: it verifies no signature, and it answers 404 for a
// deleted key because protocol_test.go told it to. MinIO is a real S3
// implementation, and it is already this adapter's declared `minio` service
// target — digest-pinned at registry/modules/system/storage-s3/module.json
// with a local_service block and the STORAGE_S3_ENDPOINT override
// module.go:31 threads into NewR2Store — and until this file nothing in the
// repository had ever run against it.
//
// Three claims only a real server can settle, one subtest each:
//
//   - the SigV4 this adapter signs is accepted by a real verifier, and a
//     corrupted secret is refused BY MINIO rather than by us;
//   - the presigned GET the 303 points at actually serves the stored bytes
//     from the origin, not from a recorder this process wrote;
//   - Delete makes the next read a genuine origin 404 with the server's own
//     NoSuchKey, rather than a fake's scripted one.
//
// The fake-backed run stays. This ADDS a real-implementation run beside it;
// the fake is what keeps the wire shape assertable with no container at all.

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gogogadget/gogogadget/internal/storage"
	storagecontract "github.com/gogogadget/gogogadget/internal/storage/contract"
	"github.com/stretchr/testify/require"
)

func TestS3StoreAgainstMinIO(t *testing.T) {
	// STORAGE_S3_ENDPOINT is this adapter's own declared endpoint override
	// (registry/modules/system/storage-s3/module.json, key
	// STORAGE_S3_ENDPOINT), so the variable that selects a real server is the
	// same one production configuration uses. Unset means nobody named a
	// server: see requireReachableEndpoint for why that is an absence and a
	// named-but-silent one is a failure.
	endpoint := os.Getenv("STORAGE_S3_ENDPOINT")
	if endpoint == "" {
		t.Skipf("[inapplicable] the MinIO run needs a real S3 server and STORAGE_S3_ENDPOINT names none " +
			"(measured 0.03 s against a warm container, plus the image pull on a cold one); CI's `test` job owns it and " +
			"sets it beside the minio service container — to run it here, start the `minio` service target this adapter " +
			"declares (the local_service block in registry/modules/system/storage-s3/module.json) and export " +
			"STORAGE_S3_ENDPOINT=http://127.0.0.1:9000 STORAGE_R2_ACCESS_KEY_ID=minioadmin " +
			"STORAGE_R2_SECRET_ACCESS_KEY=minioadmin STORAGE_R2_BUCKET=gogogadget-contract")
	}
	requireReachableEndpoint(t, endpoint)

	// Every input below is a key this adapter DECLARES for its minio target,
	// so a run of this test is a run of the documented configuration surface
	// and not of a second one invented for the test. They are required rather
	// than defaulted for the same reason the endpoint failure above is a
	// failure: the endpoint is named, so this is a configured server, and
	// guessing minioadmin for a server somebody pointed us at would turn a
	// misconfiguration into a confusing signature rejection.
	accessKey := requireNamedValue(t, "STORAGE_R2_ACCESS_KEY_ID", endpoint)
	secretKey := requireNamedValue(t, "STORAGE_R2_SECRET_ACCESS_KEY", endpoint)
	bucket := requireNamedValue(t, "STORAGE_R2_BUCKET", endpoint)

	ctx := context.Background()
	store, err := NewR2Store(ctx, "acct", accessKey, secretKey, bucket, endpoint)
	require.NoError(t, err)

	// The bucket is the one piece of setup the Store contract does not own:
	// storage.Store has no bucket lifecycle, because provisioning one is the
	// service target's job and not the request path's. Creating it through the
	// adapter's OWN client keeps this test from constructing a second,
	// differently configured S3 client whose success would prove nothing about
	// the one the application uses.
	if _, err := store.client.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: &bucket}); err != nil {
		// Owning the bucket already is the steady state on every run after
		// the first; anything else is a real setup failure.
		require.True(t,
			strings.Contains(err.Error(), "BucketAlreadyOwnedByYou") || strings.Contains(err.Error(), "BucketAlreadyExists"),
			"could not create bucket %q on %s: %v", bucket, endpoint, err)
	}

	// The positive half of the signature claim, and the cheapest possible
	// statement of it: Health is a HeadBucket, so a real verifier accepted a
	// request this adapter signed. The negative half is the last subtest.
	require.NoError(t, store.Health(ctx), "MinIO must accept the SigV4 this adapter signs")

	// The same table, with the same Options, that contract_test.go runs
	// against the in-process fake. Sharing the Options is the point: a
	// container run that needed its own relaxed expectations would not be the
	// same claim.
	storagecontract.RunWithOptions(t, func() storage.Store { return store }, presignedContractOptions())

	t.Run("the presigned GET serves the stored bytes from the origin", func(t *testing.T) {
		// The contract's own byte comparison already follows the redirect,
		// but through the Options hook — so a wrong hook is indistinguishable
		// from a correct provider. This states it directly and end to end:
		// bytes in through the adapter, 303 out, follow the Location a browser
		// would follow, and the origin hands back exactly what went in.
		key := "orgs/minio/presigned.bin"
		payload := []byte("presigned payload \x00\xff bytes")
		n, err := store.Put(ctx, key, "application/octet-stream", bytes.NewReader(payload))
		require.NoError(t, err)
		require.Equal(t, int64(len(payload)), n)

		rec := httptest.NewRecorder()
		require.NoError(t, store.Serve(ctx, rec, key, "presigned.bin", "application/octet-stream"))
		require.Equal(t, http.StatusSeeOther, rec.Code)
		location := rec.Header().Get("Location")
		require.Contains(t, location, "X-Amz-Signature=", "the redirect must carry a presigned GET")

		resp, err := http.Get(location)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "MinIO refused the presigned GET: %s", body)
		require.Equal(t, payload, body, "the presigned GET must serve the bytes Put reported")
	})

	t.Run("Delete makes the next read a real origin 404", func(t *testing.T) {
		// A fake answers 404 because the fake was written to. A real server
		// answers 404 because the object is gone, and says NoSuchKey while
		// doing it — which is the difference between asserting a deletion and
		// asserting our own map.
		key := "orgs/minio/deleted.bin"
		_, err := store.Put(ctx, key, "application/octet-stream", strings.NewReader("doomed"))
		require.NoError(t, err)
		require.NoError(t, store.Delete(ctx, key))

		rec := httptest.NewRecorder()
		require.NoError(t, store.Serve(ctx, rec, key, "deleted.bin", ""))
		resp, err := http.Get(rec.Header().Get("Location"))
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		require.Contains(t, string(body), "NoSuchKey", "MinIO must report the object gone in its own words")
	})

	t.Run("a corrupted secret is refused by MinIO", func(t *testing.T) {
		// The claim the fake cannot make at all: deterministicS3Backend never
		// checks Authorization, so the adapter could sign with an empty key
		// and every fake-backed test would still pass. Here the signature is
		// computed over a secret the server does not share, and the refusal in
		// the error is MinIO's own SignatureDoesNotMatch.
		wrong, err := NewR2Store(ctx, "acct", accessKey, secretKey+"-corrupted", bucket, endpoint)
		require.NoError(t, err, "constructing a client must not validate credentials; only the server can")
		_, err = wrong.Put(ctx, "orgs/minio/unsigned.bin", "application/octet-stream", strings.NewReader("rejected"))
		require.Error(t, err, "a request signed with the wrong secret must not be accepted")
		require.Contains(t, err.Error(), "SignatureDoesNotMatch",
			"the rejection must be the server's verdict on the signature, not a client-side guess: %v", err)
	})
}

// requireReachableEndpoint mirrors internal/db/testdb/testdb.go:103-138, and
// the rule is that function's rule with the address swapped.
//
// STORAGE_S3_ENDPOINT being SET is a request: somebody exported an address
// for this suite, and CI's `test` job does exactly that beside the service
// container that answers on it. So a named server that does not answer is a
// FAILURE and an absent variable is an ABSENCE — the skip above. Before
// testdb drew that split, every unreachable server was an absence: the stack
// was torn down between rounds, an entire package's integration tests
// skipped, `go test` printed `ok`, and that was reported as a pass.
//
// The probe is a TCP dial rather than an S3 call because unreachable is
// exactly what a dial answers. Routing it through the SDK would spend three
// retries deciding the same thing and then report it as an API error, which
// is the one distinction this function exists to keep sharp.
func requireReachableEndpoint(t *testing.T, endpoint string) {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err, "STORAGE_S3_ENDPOINT=%q is not a URL", endpoint)
	address := parsed.Host
	if parsed.Port() == "" {
		if parsed.Scheme == "https" {
			address = net.JoinHostPort(parsed.Hostname(), "443")
		} else {
			address = net.JoinHostPort(parsed.Hostname(), "80")
		}
	}
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("S3 endpoint unreachable at %s: %v\n"+
			"STORAGE_S3_ENDPOINT names this server, so one that does not answer is a broken endpoint and not an absent one. "+
			"Start the `minio` service target this adapter declares (the local_service block in "+
			"registry/modules/system/storage-s3/module.json), or unset STORAGE_S3_ENDPOINT to let this tier skip.", address, err)
		return
	}
	_ = conn.Close()
}

// requireNamedValue reads one of this adapter's declared keys, failing rather
// than skipping when it is absent. The endpoint was named, so the server is
// configured, and the remaining keys are part of that configuration: falling
// back to a guess would surface a misconfiguration as a signature rejection
// three subtests later.
func requireNamedValue(t *testing.T, key, endpoint string) string {
	t.Helper()
	value := os.Getenv(key)
	if value == "" {
		t.Fatalf("%s is unset while STORAGE_S3_ENDPOINT names %s: this adapter declares %s for its minio target, "+
			"so a named endpoint with no credentials is an incomplete configuration and not an absent server. "+
			"Export it, or unset STORAGE_S3_ENDPOINT to let this tier skip.", key, endpoint, key)
	}
	return value
}
