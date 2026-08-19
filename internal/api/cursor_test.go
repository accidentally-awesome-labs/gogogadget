package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundtrip(t *testing.T) {
	in := cursor{CreatedAt: time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC), ID: 4242}
	out, err := decodeCursor(encodeCursor(in))
	require.NoError(t, err)
	assert.True(t, in.CreatedAt.Equal(out.CreatedAt), "timestamp survives to the nanosecond: %v vs %v", in.CreatedAt, out.CreatedAt)
	assert.Equal(t, in.ID, out.ID)
}

func TestCursorIsOpaque(t *testing.T) {
	enc := encodeCursor(cursor{CreatedAt: time.Unix(0, 0), ID: 1})
	assert.NotContains(t, enc, ".", "encoded form must not leak the internal layout")
	// URL-safe: cursors travel in a query string unescaped.
	assert.NotContains(t, enc, "+")
	assert.NotContains(t, enc, "/")
	assert.NotContains(t, enc, "=")
}

func TestCursorRejectsGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"not base64":     "!!!!",
		"no separator":   "MTIzNA", // "1234"
		"bad timestamp":  "eC4x",   // "x.1"
		"bad id":         "MS54",   // "1.x"
		"empty":          "",
		"truncated pair": "MTIzNC4", // "1234."
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeCursor(in)
			assert.Error(t, err, "malformed cursor must be rejected, never silently treated as page one")
		})
	}
}
