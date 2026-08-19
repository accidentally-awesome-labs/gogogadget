// Package content parses the embedded markdown collections at startup and
// renders bodies with goldmark (GFM; raw HTML stays escaped — never enable
// html.WithUnsafe).
package content

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"gopkg.in/yaml.v3"
)

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

// Post is one blog entry.
type Post struct {
	Slug        string
	Title       string
	Description string
	Date        time.Time
	Author      string
	Draft       bool
	Body        string // rendered HTML (safe: goldmark escapes raw HTML)
}

type Blog struct {
	Posts []Post // sorted date-desc
}

type blogFrontmatter struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Date        string `yaml:"date"`
	Author      string `yaml:"author"`
	Draft       bool   `yaml:"draft"`
}

// LoadBlog parses content/blog. Drafts are excluded in production.
func LoadBlog(fsys fs.FS, production bool) (*Blog, error) {
	entries, err := fs.ReadDir(fsys, "blog")
	if err != nil {
		return nil, err
	}
	blog := &Blog{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "blog/"+e.Name())
		if err != nil {
			return nil, err
		}
		var fm blogFrontmatter
		body, err := splitFrontmatter(raw, &fm)
		if err != nil {
			return nil, fmt.Errorf("blog/%s: %w", e.Name(), err)
		}
		if fm.Draft && production {
			continue
		}
		date, err := time.Parse("2006-01-02", fm.Date)
		if err != nil {
			return nil, fmt.Errorf("blog/%s: date %q: %w", e.Name(), fm.Date, err)
		}
		html, err := render(body)
		if err != nil {
			return nil, fmt.Errorf("blog/%s: %w", e.Name(), err)
		}
		blog.Posts = append(blog.Posts, Post{
			Slug:        strings.TrimSuffix(e.Name(), ".md"),
			Title:       fm.Title,
			Description: fm.Description,
			Date:        date,
			Author:      fm.Author,
			Draft:       fm.Draft,
			Body:        html,
		})
	}
	sort.Slice(blog.Posts, func(i, j int) bool { return blog.Posts[i].Date.After(blog.Posts[j].Date) })
	return blog, nil
}

// BySlug returns the post or nil.
func (b *Blog) BySlug(slug string) *Post {
	for i := range b.Posts {
		if b.Posts[i].Slug == slug {
			return &b.Posts[i]
		}
	}
	return nil
}

// DocPage is one documentation page.
type DocPage struct {
	Slug        string
	Title       string
	Description string
	Section     string
	Weight      int
	Draft       bool
	Body        string

	// raw is the markdown source (frontmatter stripped) — the searchable
	// text. Unexported: rendering uses Body, search uses raw.
	raw string
}

type DocSection struct {
	Name  string
	Pages []DocPage // ordered by weight
}

type Docs struct {
	Pages    []DocPage // ordered by weight overall
	Sections []DocSection
}

type docFrontmatter struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Section     string `yaml:"section"`
	Weight      int    `yaml:"weight"`
	Draft       bool   `yaml:"draft"`
}

// LoadDocs parses content/docs, grouped by section and ordered by weight.
// Drafts are excluded in production.
func LoadDocs(fsys fs.FS, production bool) (*Docs, error) {
	entries, err := fs.ReadDir(fsys, "docs")
	if err != nil {
		return nil, err
	}
	docs := &Docs{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := fs.ReadFile(fsys, "docs/"+e.Name())
		if err != nil {
			return nil, err
		}
		var fm docFrontmatter
		body, err := splitFrontmatter(raw, &fm)
		if err != nil {
			return nil, fmt.Errorf("docs/%s: %w", e.Name(), err)
		}
		if fm.Draft && production {
			continue
		}
		html, err := render(body)
		if err != nil {
			return nil, fmt.Errorf("docs/%s: %w", e.Name(), err)
		}
		docs.Pages = append(docs.Pages, DocPage{
			Slug:        strings.TrimSuffix(e.Name(), ".md"),
			Title:       fm.Title,
			Description: fm.Description,
			Section:     fm.Section,
			Weight:      fm.Weight,
			Draft:       fm.Draft,
			Body:        html,
			raw:         string(body),
		})
	}
	sort.Slice(docs.Pages, func(i, j int) bool { return docs.Pages[i].Weight < docs.Pages[j].Weight })

	sectionIdx := map[string]int{}
	for _, p := range docs.Pages {
		idx, ok := sectionIdx[p.Section]
		if !ok {
			docs.Sections = append(docs.Sections, DocSection{Name: p.Section})
			idx = len(docs.Sections) - 1
			sectionIdx[p.Section] = idx
		}
		docs.Sections[idx].Pages = append(docs.Sections[idx].Pages, p)
	}
	return docs, nil
}

// BySlug returns the page or nil.
func (d *Docs) BySlug(slug string) *DocPage {
	for i := range d.Pages {
		if d.Pages[i].Slug == slug {
			return &d.Pages[i]
		}
	}
	return nil
}

// splitFrontmatter splits "---\n<yaml>\n---\n<body>" and renders nothing.
func splitFrontmatter(raw []byte, out any) ([]byte, error) {
	s := string(raw)
	if !strings.HasPrefix(s, "---") {
		return nil, fmt.Errorf("missing frontmatter")
	}
	rest := s[3:]
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter")
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), out); err != nil {
		return nil, err
	}
	body := rest[end+4:]
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	return []byte(body), nil
}

func render(src []byte) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
