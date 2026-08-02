package content

import (
	"io/fs"
	"regexp"
	"testing"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var docsLinkRe = regexp.MustCompile(`\]\(/docs/([a-z0-9-]+)\)`)

// TestDocsInternalLinksResolve asserts every internal /docs/<slug> link in
// every shipped markdown file points at a page that exists.
func TestDocsInternalLinksResolve(t *testing.T) {
	docs, err := LoadDocs(contentfs.FS, true)
	require.NoError(t, err)
	known := map[string]bool{}
	for _, p := range docs.Pages {
		known[p.Slug] = true
	}

	err = fs.WalkDir(contentfs.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := fs.ReadFile(contentfs.FS, path)
		if err != nil {
			return err
		}
		for _, m := range docsLinkRe.FindAllSubmatch(raw, -1) {
			slug := string(m[1])
			assert.True(t, known[slug], "%s links to /docs/%s which does not exist", path, slug)
		}
		return nil
	})
	require.NoError(t, err)
}

// TestDocsShipsTwentyPages guards the docs inventory and sidebar ordering.
func TestDocsShipsTwentyPages(t *testing.T) {
	docs, err := LoadDocs(contentfs.FS, true)
	require.NoError(t, err)
	require.Len(t, docs.Pages, 20, "the docs section ships exactly 20 pages")

	// Weights strictly increase; sections are in order.
	lastWeight := 0
	wantSections := []string{"Start", "Core", "Features", "Guides"}
	sectionSeen := map[string]int{}
	for _, p := range docs.Pages {
		assert.Greater(t, p.Weight, lastWeight, "weights must strictly increase (%s)", p.Slug)
		lastWeight = p.Weight
		sectionSeen[p.Section]++
	}
	assert.Equal(t, 2, sectionSeen["Start"])
	assert.Equal(t, 3, sectionSeen["Core"])
	assert.Equal(t, 9, sectionSeen["Features"])
	assert.Equal(t, 6, sectionSeen["Guides"])
	require.Len(t, docs.Sections, 4)
	for i, name := range wantSections {
		assert.Equal(t, name, docs.Sections[i].Name)
	}

	// Frontmatter completeness: every page has title, description, section.
	for _, p := range docs.Pages {
		assert.NotEmpty(t, p.Title, p.Slug)
		assert.NotEmpty(t, p.Description, p.Slug)
		assert.NotEmpty(t, p.Section, p.Slug)
		assert.NotEmpty(t, p.Body, p.Slug)
	}
}
