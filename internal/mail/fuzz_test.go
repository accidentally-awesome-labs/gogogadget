package mail

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FuzzSanitizeFilename guards the trust boundary between user-supplied
// addresses (msg.To) and on-disk attachment filenames. Invariant: the output
// NEVER contains a path separator, a parent-dir reference, or a NUL byte —
// so a crafted address cannot escape the sender's output directory.
func FuzzSanitizeFilename(f *testing.F) {
	for _, s := range []string{
		"demo@gogogadget.dev",
		"new@example.com",
		"../etc/passwd",
		"..\\..\\windows\\system32",
		"../../..",
		"..",
		"...",
		"\x00null\x00byte",
		"a/b\\c/d",
		"ünïcode-名前@example.com",
		" leading and trailing ",
		"",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		out := sanitizeFilename(s)
		assert.NotContains(t, out, "/", "input %q", s)
		assert.NotContains(t, out, "\\", "input %q", s)
		assert.NotContains(t, out, "..", "input %q", s)
		assert.NotContains(t, out, "\x00", "input %q", s)
		assert.False(t, strings.ContainsRune(out, 0), "input %q", s)
	})
}
