package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5"
)

// Idempotency for unsafe calls. A client whose POST times out cannot tell
// whether the work happened; without a key its only options are to retry and
// risk a duplicate, or not retry and risk losing the write. With a key the
// retry is safe: the first request's outcome is replayed verbatim.
//
// Shape (deliberately the one Stripe made everyone fluent in):
//
//   - No Idempotency-Key header      → nothing changes; the feature is opt-in.
//   - New key                        → execute, store the outcome, return it.
//   - Same key, same request         → replay the stored status and body,
//     with Idempotency-Replayed: true.
//   - Same key, different request    → 409 idempotency_conflict. A key names
//     one intended operation; silently serving a different one would be worse
//     than any error.
//   - Same key, still running        → 409 idempotency_in_progress, because
//     the honest answer is "ask again", not a second execution.
//
// Retention is 24h (the janitor sweeps): long enough for any sane retry
// schedule, short enough that the table stays small.

const (
	idempotencyHeader    = "Idempotency-Key"
	idempotencyMaxKeyLen = 255
)

// Idempotent wraps an unsafe handler. It must run INSIDE RequireAPIToken —
// the key is scoped to the authenticated organization, not to the token, so
// a client that rotates credentials mid-retry still deduplicates.
func (m *Middleware) Idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(idempotencyHeader)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > idempotencyMaxKeyLen {
			WriteError(w, http.StatusBadRequest, "invalid_idempotency_key",
				"Idempotency-Key must be 255 characters or fewer.")
			return
		}

		// The body is read here and handed back to the handler: hashing it is
		// what makes "same key, different request" detectable.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_json", "Could not read the request body.")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		org := identity.OrgFrom(r.Context())
		sum := sha256.Sum256(body)
		claim := sqlc.ClaimIdempotencyKeyParams{
			OrgID:  org.OrgID,
			Key:         key,
			Endpoint:    r.Method + " " + r.URL.Path,
			RequestHash: hex.EncodeToString(sum[:]),
		}

		// The primary key conflict IS the lock: two concurrent retries race to
		// insert, exactly one wins, and the loser never executes the handler.
		_, err = m.Q.ClaimIdempotencyKey(r.Context(), claim)
		if errors.Is(err, pgx.ErrNoRows) {
			m.replay(w, r, claim)
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "internal_error", "Idempotency lookup failed.")
			return
		}

		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// 5xx says nothing about whether the work should happen, so the claim
		// is dropped rather than stored: pinning a transient failure to the
		// key would poison it for the whole retention window — the precise
		// case the client is retrying to escape.
		if rec.status >= 500 {
			if err := m.Q.ReleaseIdempotencyKey(r.Context(), sqlc.ReleaseIdempotencyKeyParams{
				OrgID: claim.OrgID, Key: claim.Key,
			}); err != nil {
				m.logf("idempotency release failed", err)
			}
			return
		}
		if err := m.Q.CompleteIdempotencyKey(r.Context(), sqlc.CompleteIdempotencyKeyParams{
			OrgID: claim.OrgID, Key: claim.Key,
			Status:   int32(rec.status),
			Response: rec.body.Bytes(),
		}); err != nil {
			// The write happened; failing the response now would invite a
			// retry that duplicates it. Log and return the real outcome.
			m.logf("idempotency store failed", err)
		}
	})
}

// replay serves the stored outcome of an earlier request with this key.
func (m *Middleware) replay(w http.ResponseWriter, r *http.Request, claim sqlc.ClaimIdempotencyKeyParams) {
	prior, err := m.Q.GetIdempotencyKey(r.Context(), sqlc.GetIdempotencyKeyParams{
		OrgID: claim.OrgID, Key: claim.Key,
	})
	if err != nil {
		// Raced with the janitor (or a release): the key is free again, and
		// the honest answer is "retry", not a fabricated success.
		WriteError(w, http.StatusConflict, "idempotency_in_progress",
			"A request with this Idempotency-Key is in flight. Retry shortly.")
		return
	}
	if prior.Endpoint != claim.Endpoint || prior.RequestHash != claim.RequestHash {
		WriteError(w, http.StatusConflict, "idempotency_conflict",
			"This Idempotency-Key was already used for a different request. Use a new key.")
		return
	}
	if prior.Status == 0 {
		WriteError(w, http.StatusConflict, "idempotency_in_progress",
			"A request with this Idempotency-Key is still running. Retry shortly.")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(int(prior.Status))
	_, _ = w.Write(prior.Response)
}

func (m *Middleware) logf(msg string, err error) {
	if m.Log != nil {
		m.Log.Error(msg, "error", err)
	}
}

// recorder buffers the handler's response so it can be stored alongside the
// key. API payloads are small JSON documents by construction (the 10 MB
// request cap is upstream), so buffering costs nothing worth optimizing.
type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// Unwrap keeps http.NewResponseController working through the wrapper.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
