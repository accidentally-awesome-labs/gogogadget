package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Store talks to an S3-compatible API. The endpoint may point at R2, MinIO,
// or another S3-compatible service; the adapter owns SDK details.
type R2Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewR2Store builds the client with static credentials. endpoint may be empty
// (defaults to the R2 URL for the account) for R2, or an explicit S3 base URL.
func NewR2Store(ctx context.Context, accountID, accessKeyID, secretAccessKey, bucket, endpoint string) (*R2Store, error) {
	if endpoint == "" {
		endpoint = "https://" + accountID + ".r2.cloudflarestorage.com"
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, "")),
	)
	if err != nil {
		return nil, err
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = &endpoint
		o.UsePathStyle = true // R2 (and MinIO) require path-style addressing
	})
	return &R2Store{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  bucket,
	}, nil
}

func (s *R2Store) Put(ctx context.Context, key, contentType string, r io.Reader) (int64, error) {
	// S3 PutObject needs Content-Length; buffer the (already size-capped)
	// upload. The global 10 MB cap bounds memory.
	body, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &s.bucket, Key: &key, ContentType: &contentType, Body: bytes.NewReader(body),
	}); err != nil {
		return 0, err
	}
	return int64(len(body)), nil
}

func (s *R2Store) Serve(ctx context.Context, w http.ResponseWriter, key, filename, contentType string) error {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &s.bucket, Key: &key,
	}, s3.WithPresignExpires(15*time.Minute))
	if err != nil {
		return err
	}
	// 303 to a short-lived presigned GET: the object streams straight from
	// the provider, never through this process.
	w.Header().Set("Location", req.URL)
	w.WriteHeader(http.StatusSeeOther)
	return nil
}

// ServeInline is identical to Serve for R2: the presigned GET streams from
// the provider with no Content-Disposition, so the stored content type
// governs and an image renders in the page.
func (s *R2Store) ServeInline(ctx context.Context, w http.ResponseWriter, key, contentType string) error {
	return s.Serve(ctx, w, key, "", contentType)
}

func (s *R2Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key})
	return err
}
func (s *R2Store) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
	return err
}
