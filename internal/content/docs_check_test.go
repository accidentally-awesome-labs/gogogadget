package content

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// docsLinkRe matches an in-app docs link. The optional trailing group covers
// `/docs/security#csrf` and `/docs/security/`, which resolve to the same page
// and were previously invisible to this check — a broken anchor link is still
// a broken link.
var docsLinkRe = regexp.MustCompile(`\]\(/docs/([a-z0-9-]+)[/#)]`)

// readmeDocsLinkRe matches the repository README's relative links into the
// docs corpus. The README is not embedded, so it is read from disk; it is the
// entry point every reader hits first, and a dead link there is the one that
// costs the most.
var readmeDocsLinkRe = regexp.MustCompile(`\]\(content/docs/([a-z0-9-]+)\.md\)`)

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

// TestReadmeDocsLinksResolve asserts every content/docs/<slug>.md link in the
// repository README points at a page that actually ships. The README is the
// first thing a reader opens and the one file the docs pipeline does not
// embed, so nothing else would catch a rename here.
func TestReadmeDocsLinksResolve(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	matches := readmeDocsLinkRe.FindAllSubmatch(raw, -1)
	require.NotEmpty(t, matches, "the README must link into the docs corpus")
	for _, m := range matches {
		slug := string(m[1])
		_, statErr := os.Stat(filepath.Join("..", "..", "content", "docs", slug+".md"))
		assert.NoError(t, statErr, "README links to content/docs/%s.md which does not exist", slug)
	}
}

// TestDocsInventory guards the docs inventory count and sidebar ordering.
func TestDocsInventory(t *testing.T) {
	docs, err := LoadDocs(contentfs.FS, true)
	require.NoError(t, err)
	require.Len(t, docs.Pages, 37, "the docs section ships exactly 37 pages")

	// Weights strictly increase; sections are in order.
	lastWeight := 0
	wantSections := []string{"Start", "Core", "Features", "Guides", "Modules"}
	sectionSeen := map[string]int{}
	for _, p := range docs.Pages {
		assert.Greater(t, p.Weight, lastWeight, "weights must strictly increase (%s)", p.Slug)
		lastWeight = p.Weight
		sectionSeen[p.Section]++
	}
	assert.Equal(t, 2, sectionSeen["Start"])
	assert.Equal(t, 4, sectionSeen["Core"])
	assert.Equal(t, 16, sectionSeen["Features"])
	assert.Equal(t, 7, sectionSeen["Guides"])
	assert.Equal(t, 8, sectionSeen["Modules"])
	require.Len(t, docs.Sections, 5)
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
