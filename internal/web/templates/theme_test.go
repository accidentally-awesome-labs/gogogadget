package templates

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Email is the one surface that cannot read a token: mail clients strip <style>
// and do not resolve custom properties, so emailStyle inlines the hex. That
// makes it the one place a rebrand silently misses - input.css changes, every
// page turns green, and the welcome email keeps sending indigo. Nothing caught
// that, because the two files agree only by memory.
//
// This test pins the agreement. It reads the light-mode value of each token out
// of input.css and asserts the corresponding emailStyle field carries it, so a
// rebrand that forgets the email fails here with the field name in the message.
func TestEmailStyleTracksTheLightThemeTokens(t *testing.T) {
	sheet := readInputCSS(t)

	for _, want := range []struct {
		token string
		field string
		value string
	}{
		{"--color-brand-600", "Brand", emailStyle.Brand},
		{"--color-brand-600", "Link", emailStyle.Link},
		{"--color-brand-600", "DigestLink", emailStyle.DigestLink},
		{"--color-surface", "Card", emailStyle.Card},
		{"--color-surface-raised", "Page", emailStyle.Page},
		{"--color-fg", "Card", emailStyle.Card},
		{"--color-fg-muted", "DigestBody", emailStyle.DigestBody},
		{"--color-border", "DigestRow", emailStyle.DigestRow},
	} {
		hex := lightTokenValue(t, sheet, want.token)
		assert.Containsf(t, want.value, hex,
			"input.css declares %s: %s, but emailStyle.%s does not carry it - "+
				"a rebrand that edits only input.css leaves transactional email on the old palette",
			want.token, hex, want.field)
	}

	// The card's corner is the same decision as every other surface radius, and
	// it is the one non-colour value email has to restate.
	radius := lightTokenValue(t, sheet, "--radius-surface")
	assert.Equal(t, "0.75rem", radius,
		"the email card inlines 12px for --radius-surface; a different value needs emailStyle.Card updated too")
	assert.Contains(t, emailStyle.Card, "border-radius:12px",
		"emailStyle.Card must restate --radius-surface in px: email cannot resolve rem against a root font size it does not control")
}

// lightTokenValue returns a token's declared value from outside the .dark block.
// Email has no dark mode - a mail client renders whatever the message inlines -
// so the dark override is not the value to compare against.
//
// The block is cut by hand rather than through withoutDarkBlock, which reads the
// BUILT sheet: input.css also carries `@custom-variant dark (&:where(.dark, …))`
// at the top, and a scan that treats the first ".dark" as a rule start swallows
// the whole @theme block after it - which is where the brand ramp lives.
func lightTokenValue(t *testing.T, sheet, token string) string {
	t.Helper()

	light := sheet
	if start := strings.Index(light, "  .dark {"); start >= 0 {
		if end := strings.Index(light[start:], "\n  }"); end > 0 {
			light = light[:start] + light[start+end:]
		}
	}

	m := regexp.MustCompile(regexp.QuoteMeta(token) + `:\s*([^;\n]+)`).FindStringSubmatch(light)
	require.Lenf(t, m, 2, "input.css declares no light-mode %s", token)

	value := strings.TrimSpace(m[1])
	if inner := regexp.MustCompile(`^var\((--[a-z0-9-]+)\)$`).FindStringSubmatch(value); inner != nil {
		return lightTokenValue(t, sheet, inner[1])
	}
	return value
}
