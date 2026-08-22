// Package storage is the file-storage seam: handlers talk to Store, never to
// an S3 SDK. R2 is the configured implementation; DevStore is the zero-account
// default (disk under tmp/uploads). Mirrors mail.Sender/DevSender.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"regexp"
)

// Store persists and retrieves binary objects. Implementations: R2 (S3 API)
// and DevStore (local disk, dev/test only).
type Store interface {
	// Put writes the object read from r and returns its size.
	Put(ctx context.Context, key, contentType string, r io.Reader) (sizeBytes int64, err error)
	// Serve delivers the object to the client. R2: 303 to a presigned GET
	// (15 min). Dev: stream from disk. Content-Disposition is always
	// attachment — untrusted user uploads are never rendered inline.
	Serve(ctx context.Context, w http.ResponseWriter, key, filename, contentType string) error
	// ServeInline delivers an object for rendering IN the page. Only for
	// content media that passed the image allowlist at upload (see
	// handleAdminMediaUpload); never for a user upload whose content type
	// came from the client.
	ServeInline(ctx context.Context, w http.ResponseWriter, key, contentType string) error
	Delete(ctx context.Context, key string) error
}

// extRe captures a short trailing extension of the ORIGINAL filename.
var extRe = regexp.MustCompile(`\.[a-zA-Z0-9]{1,8}$`)

// NewKey builds an unguessable, collision-free storage key:
// orgs/{orgID}/{32 hex random}{.ext}. The org prefix makes per-org listing or
// lifecycle trivial on the bucket side.
func NewKey(orgID, originalFilename string) string {
	return "orgs/" + orgID + "/" + randomObjectName(originalFilename)
}

// NewContentKey builds a platform-scoped media key: content/{32 hex}{.ext}.
// Deliberately NOT NewKey's org prefix — content media belongs to the
// platform, not to a tenant (exportKey in internal/jobs is the precedent).
func NewContentKey(originalFilename string) string {
	return "content/" + randomObjectName(originalFilename)
}

// randomObjectName is 32 hex characters plus the original extension.
func randomObjectName(originalFilename string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand never fails on supported platforms; degrade to a
		// time-free constant rather than panicking mid-request.
		for i := range b {
			b[i] = byte(i * 7)
		}
	}
	ext := ""
	if m := extRe.FindString(path.Base(originalFilename)); m != "" {
		ext = m
	}
	return hex.EncodeToString(b) + ext
}
