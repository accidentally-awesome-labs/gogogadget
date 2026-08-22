package content

import "time"

// Release is the changelog view model: one live entry of the "release"
// content type, projected from Entry.AsRelease.
//
// Releases are a separate type from the blog on purpose: a changelog answers
// "what changed in the product", is read newest-first as a single scrollable
// page, and every entry needs a stable anchor to link a customer at. A blog
// post is a document with its own URL and a narrative.
type Release struct {
	Slug string // "2026-08-19"
	// Anchor is the element id and URL fragment. Prefixed because a bare
	// "2026-08-19" is a legal HTML id but NOT a legal CSS selector — an id
	// cannot start with a digit, so document.querySelector("#2026-08-19")
	// throws and no stylesheet can target it.
	Anchor  string
	Title   string
	Date    time.Time
	Summary string
	Body    string // rendered HTML
}

// releaseFrontmatter is the seed corpus's frontmatter. There is no draft
// flag: an entry exists once the work ships.
type releaseFrontmatter struct {
	Title   string `yaml:"title"`
	Date    string `yaml:"date"`
	Summary string `yaml:"summary"`
}
