// Package content owns the app's editorial surface: the embedded docs
// collection parsed at startup, the database-backed CMS for every registered
// content type (see types.go and cms.go), and the goldmark renderer they
// share (GFM; raw HTML stays escaped — never enable html.WithUnsafe).
package content

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
	"gopkg.in/yaml.v3"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(renderer.WithNodeRenderers(
		// Priority beats the default HTML renderer's 1000.
		util.Prioritized(focusableCodeBlocks{}, 100),
	)),
)

// focusableCodeBlocks renders code blocks as goldmark's default renderer does,
// plus tabindex="0" on the <pre>.
//
// A <pre> that overflows horizontally is a scrollable region, and a scrollable
// region that cannot be focused cannot be scrolled by keyboard at all — axe
// reports it as scrollable-region-focusable (WCAG 2.1.1), which the docs
// accessibility sweep fails on. Line length is not the fix: the next long
// command re-breaks it. tabindex="0" needs no CSS, because input.css already
// gives `[tabindex]:focus-visible` the global focus ring.
type focusableCodeBlocks struct{}

func (focusableCodeBlocks) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindCodeBlock, renderFocusableCodeBlock)
	reg.Register(ast.KindFencedCodeBlock, renderFocusableFencedCodeBlock)
}

func renderFocusableCodeBlock(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code></pre>\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<pre tabindex="0"><code>`)
	writeCodeLines(w, source, n)
	return ast.WalkContinue, nil
}

func renderFocusableFencedCodeBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</code></pre>\n")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<pre tabindex="0"><code`)
	if language := node.(*ast.FencedCodeBlock).Language(source); language != nil {
		_, _ = w.WriteString(` class="language-`)
		html.DefaultWriter.Write(w, language)
		_, _ = w.WriteString(`"`)
	}
	_ = w.WriteByte('>')
	writeCodeLines(w, source, node)
	return ast.WalkContinue, nil
}

// writeCodeLines mirrors the default renderer's writeLines: the body is raw
// text, escaped, never parsed as HTML.
func writeCodeLines(w util.BufWriter, source []byte, n ast.Node) {
	for i := range n.Lines().Len() {
		line := n.Lines().At(i)
		html.DefaultWriter.RawWrite(w, line.Value(source))
	}
}

// Post is the blog view model: one live entry of the "post" content type,
// projected from Entry.AsPost. The markdown seed corpus parses into the same
// shape (see import.go).
type Post struct {
	Slug        string
	Title       string
	Description string
	Date        time.Time
	Author      string
	Draft       bool
	Body        string // rendered HTML (safe: goldmark escapes raw HTML)
}

// blogFrontmatter is the seed corpus's frontmatter. A post marked draft is
// simply not imported.
type blogFrontmatter struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Date        string `yaml:"date"`
	Author      string `yaml:"author"`
	Draft       bool   `yaml:"draft"`
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
		html, err := Render(body)
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

// Render turns markdown into HTML on the shared goldmark instance (GFM, raw
// HTML escaped). Exported so the admin save and preview paths produce exactly
// what the public page will show.
func Render(src []byte) (string, error) {
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
