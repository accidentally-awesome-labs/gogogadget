package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateTokenFormatAndUniqueness(t *testing.T) {
	plaintext, hash, err := GenerateToken()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(plaintext, "ggg_"), "token must carry the ggg_ prefix, got %q", plaintext)
	assert.Equal(t, HashToken(plaintext), hash, "returned hash must be the storage form of the plaintext")

	plaintext2, hash2, err := GenerateToken()
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, plaintext2, "two generated tokens must differ")
	assert.NotEqual(t, hash, hash2, "two generated hashes must differ")
}

func TestHashTokenIsSHA256Hex(t *testing.T) {
	for _, input := range []string{"", "ggg_abc", "ggg_" + strings.Repeat("x", 43)} {
		sum := sha256.Sum256([]byte(input))
		want := hex.EncodeToString(sum[:])
		assert.Equal(t, want, HashToken(input), "HashToken(%q)", input)
	}
}

func TestScopeSatisfies(t *testing.T) {
	cases := []struct {
		have, want string
		ok         bool
	}{
		{"read", "read", true},
		{"write", "write", true},
		{"write", "read", true},  // write satisfies read
		{"read", "write", false}, // read never satisfies write
		{"", "read", false},
		{"admin", "read", false},  // unknown stored scope satisfies nothing
		{"read", "admin", false},  // unknown required scope: nothing satisfies it
		{"write", "admin", false}, // even write cannot satisfy an unknown scope
		{"admin", "admin", false}, // unknown want is refused regardless
		{"", "", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.ok, scopeSatisfies(tc.have, tc.want),
			"scopeSatisfies(have=%q, want=%q)", tc.have, tc.want)
	}
}

func TestWriteErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusForbidden, "forbidden", "nope")

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "forbidden", body.Error.Code)
	assert.Equal(t, "nope", body.Error.Message)

	// The wire shape is exactly {"error":{"code","message"}} — no extra keys.
	var raw map[string]map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	require.Len(t, raw, 1)
	assert.Equal(t, map[string]string{"code": "forbidden", "message": "nope"}, raw["error"])
}
