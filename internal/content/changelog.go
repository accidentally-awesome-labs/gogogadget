package content

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// Release is one dated changelog entry.
//
// Releases are a separate collection from the blog on purpose: a changelog
// answers "what changed in the product", is read newest-first as a single
// scrollable page, and every entry needs a stable anchor to link a customer
// at. A blog post is a document with its own URL and a narrative.
type Release struct {
	Slug string // "2026-08-19", from the filename
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

// Changelog is every release, newest first.
type Changelog struct {
	Releases []Release
}

type releaseFrontmatter struct {
	Title   string `yaml:"title"`
	Date    string `yaml:"date"`
	Summary string `yaml:"summary"`
}

// LoadChangelog parses content/changelog. There is no draft flag: an entry
// exists once the work ships, and a half-written release note is a file you
// have not committed yet.
func LoadChangelog(fsys fs.FS) (*Changelog, error) {
	entries, err := fs.ReadDir(fsys, "changelog")
	if err != nil {
		return nil, err
	}
	log := &Changelog{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "changelog/"+e.Name())
		if err != nil {
			return nil, err
		}
		var fm releaseFrontmatter
		body, err := splitFrontmatter(raw, &fm)
		if err != nil {
			return nil, fmt.Errorf("changelog/%s: %w", e.Name(), err)
		}
		date, err := time.Parse("2006-01-02", fm.Date)
		if err != nil {
			return nil, fmt.Errorf("changelog/%s: date %q: %w", e.Name(), fm.Date, err)
		}
		if fm.Title == "" {
			return nil, fmt.Errorf("changelog/%s: title is required", e.Name())
		}
		html, err := render(body)
		if err != nil {
			return nil, fmt.Errorf("changelog/%s: %w", e.Name(), err)
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		log.Releases = append(log.Releases, Release{
			Slug:    slug,
			Anchor:  "release-" + slug,
			Title:   fm.Title,
			Date:    date,
			Summary: fm.Summary,
			Body:    html,
		})
	}
	sort.Slice(log.Releases, func(i, j int) bool { return log.Releases[i].Date.After(log.Releases[j].Date) })
	return log, nil
}

// Latest returns the newest release, or nil when there is none. Nil-safe on
// the receiver: callers treat the collection as optional.
func (c *Changelog) Latest() *Release {
	if c == nil || len(c.Releases) == 0 {
		return nil
	}
	return &c.Releases[0]
}
