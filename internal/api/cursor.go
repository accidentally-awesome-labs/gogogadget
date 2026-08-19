package api

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Cursors are opaque on purpose. Clients must treat them as blobs they echo
// back, never parse — that is what lets the keyset widen (add a sort column,
// change the tiebreak) without a v2 of the API. The encoding is deliberately
// boring: "<unix_nanos>.<id>", base64url, no padding.
//
// Not signed: a cursor only selects a position inside a result set the caller
// is already authorized to read (the query is org-scoped by the token), so a
// forged cursor can at worst point at a different page of the caller's own
// projects. Signing would buy nothing and cost a key.

var errBadCursor = errors.New("malformed cursor")

type cursor struct {
	CreatedAt time.Time
	ID        int64
}

func encodeCursor(c cursor) string {
	raw := strconv.FormatInt(c.CreatedAt.UTC().UnixNano(), 10) + "." + strconv.FormatInt(c.ID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursor{}, errBadCursor
	}
	nanos, id, ok := strings.Cut(string(raw), ".")
	if !ok {
		return cursor{}, errBadCursor
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return cursor{}, errBadCursor
	}
	i, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return cursor{}, errBadCursor
	}
	return cursor{CreatedAt: time.Unix(0, n).UTC(), ID: i}, nil
}
