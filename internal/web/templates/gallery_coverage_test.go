package templates

import (
	"regexp"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both claims here are about the gallery page, so they live with the module
// that renders it. They were in ggg/system/server's designsystem_test.go,
// calling Gallery() - which ggg/page/dev-gallery owns and neither
// `ggg/profile/minimal` nor `ggg/profile/web` installs - so the shell's own
// test payload did not compile in either of those projects.

// The gallery is the catalog's reference surface: if a component is installed
// but never rendered there, nobody reviewing the design system - human or agent
// - can see it, and no visual or accessibility gate covers it. Comparing the
// rendered data-ui markers against the generated registry closes the gap that a
// hand-kept list leaves open.
func TestGalleryCoversEveryInstalledComponent(t *testing.T) {
	html := renderComponent(t, Gallery())
	rendered := renderedComponentMarkers(html)
	require.NotEmpty(t, ui.ComponentRegistry, "no components are installed")

	var missing []string
	for _, c := range ui.ComponentRegistry {
		if _, ok := rendered[c.Name]; !ok {
			missing = append(missing, c.Name+" ("+string(c.Family)+")")
		}
	}
	assert.Empty(t, missing, "installed components the gallery never renders")

	for name := range rendered {
		_, ok := ui.ComponentByName(name)
		assert.True(t, ok, "gallery renders %q, which no installed module declares", name)
	}
}

func renderedComponentMarkers(html string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, m := range regexp.MustCompile(`data-ui="([a-z0-9-]+)"`).FindAllStringSubmatch(html, -1) {
		out[m[1]] = struct{}{}
	}
	return out
}

// A progressbar or meter with no accessible name reports a number with no
// subject. The gallery is the one page that renders every component, so it is
// where an unnamed one shows up - and it did: three meters shipped nameless.
func TestEveryProgressAndMeterInTheGalleryIsNamed(t *testing.T) {
	html := renderComponent(t, Gallery())
	for _, role := range []string{"progressbar", "meter"} {
		for _, tag := range findRoleTags(html, role) {
			assert.Contains(t, tag, "aria-label=",
				"a %s with no accessible name announces a number with no subject", role)
		}
	}
}

// findRoleTags returns the opening tags carrying a given role.
func findRoleTags(html, role string) []string {
	var out []string
	needle := `role="` + role + `"`
	for i := 0; i < len(html); {
		at := strings.Index(html[i:], needle)
		if at < 0 {
			break
		}
		at += i
		start := strings.LastIndex(html[:at], "<")
		end := strings.Index(html[at:], ">")
		if start >= 0 && end >= 0 {
			out = append(out, html[start:at+end])
		}
		i = at + len(needle)
	}
	return out
}
