package content

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testFS = fstest.MapFS{
	"blog/one.md": &fstest.MapFile{Data: []byte(`---
title: First Post
description: The first one.
date: 2026-01-10
author: A
draft: false
---

# Heading

Some **bold** text and <script>alert(1)</script> raw HTML.
`)},
	"blog/two.md": &fstest.MapFile{Data: []byte(`---
title: Second Post
description: The second one.
date: 2026-01-20
author: B
---

Body two.
`)},
	"blog/draft.md": &fstest.MapFile{Data: []byte(`---
title: Draft Post
description: Hidden in prod.
date: 2026-01-30
author: C
draft: true
---

Draft body.
`)},
	"docs/getting-started.md": &fstest.MapFile{Data: []byte(`---
title: Getting started
description: Start here.
section: Start
weight: 2
---

Install things.
`)},
	"docs/index.md": &fstest.MapFile{Data: []byte(`---
title: Overview
description: What it is.
section: Start
weight: 1
---

Overview body.
`)},
}

// The seed corpus parses into the blog view model: frontmatter read, GFM
// rendered, raw HTML escaped, drafts skipped.
func TestParsePosts(t *testing.T) {
	posts, err := ParsePosts(testFS)
	require.NoError(t, err)
	require.Len(t, posts, 2, "a draft in the corpus is a file you have not finished")
	for _, p := range posts {
		assert.NotEqual(t, "draft", p.Slug)
	}

	// Sorted date-desc.
	assert.Equal(t, "Second Post", posts[0].Title)
	assert.Equal(t, "First Post", posts[1].Title)

	first := posts[1]
	assert.Equal(t, "one", first.Slug)
	assert.Equal(t, "A", first.Author)
	assert.Contains(t, first.Body, "<h1>Heading</h1>")
	assert.Contains(t, first.Body, "<strong>bold</strong>")
	// goldmark runs without WithUnsafe. This is the whole XSS story for the
	// @templ.Raw a public page does on a rendered body.
	assert.NotContains(t, first.Body, "<script>")
	assert.Contains(t, first.Body, "raw HTML omitted")
}

func TestParsePostsNamesTheBrokenFile(t *testing.T) {
	bad := fstest.MapFS{"blog/broken.md": &fstest.MapFile{Data: []byte("no frontmatter here\n")}}
	_, err := ParsePosts(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blog/broken.md")
}

func TestLoadDocsGroupingAndOrder(t *testing.T) {
	docs, err := LoadDocs(testFS, false)
	require.NoError(t, err)
	require.Len(t, docs.Pages, 2)

	// Ordered by weight overall.
	assert.Equal(t, "index", docs.Pages[0].Slug)
	assert.Equal(t, "getting-started", docs.Pages[1].Slug)

	// Grouped by section.
	require.Len(t, docs.Sections, 1)
	assert.Equal(t, "Start", docs.Sections[0].Name)
	require.Len(t, docs.Sections[0].Pages, 2)
	assert.Equal(t, "index", docs.Sections[0].Pages[0].Slug)
}

// A <pre> that overflows horizontally is a scrollable region, and axe fails an
// unfocusable one (scrollable-region-focusable, WCAG 2.1.1) — which is how the
// docs accessibility sweep caught this. Every code block the renderer emits
// carries tabindex="0", so no page's line lengths can reintroduce it.
func TestRenderMakesCodeBlocksKeyboardFocusable(t *testing.T) {
	for name, src := range map[string]string{
		"fenced":   "```sh\nggg provider set --slot ggg/mail\n```\n",
		"unfenced": "    ggg provider set --slot ggg/mail\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := Render([]byte(src))
			require.NoError(t, err)
			assert.Contains(t, out, `<pre tabindex="0">`)
			assert.NotContains(t, out, "<pre>", "an unfocusable code block cannot be scrolled by keyboard")
			assert.Contains(t, out, "ggg provider set --slot ggg/mail")
		})
	}
}

func TestRenderKeepsCodeBlockBodiesEscaped(t *testing.T) {
	out, err := Render([]byte("```html\n<script>alert(1)</script>\n```\n"))
	require.NoError(t, err)
	assert.Contains(t, out, `<pre tabindex="0"><code class="language-html">`)
	assert.Contains(t, out, "&lt;script&gt;", "a code body is text, never markup")
	assert.NotContains(t, out, "<script>")
}

func TestRSS(t *testing.T) {
	posts, err := ParsePosts(testFS)
	require.NoError(t, err)
	items := make([]FeedItem, 0, len(posts))
	for _, p := range posts {
		items = append(items, FeedItem{
			Title: p.Title, Link: "https://example.com/blog/" + p.Slug,
			Description: p.Description, Date: p.Date,
		})
	}
	feed, err := RSS("https://example.com", "GoGoGadget Blog", "Product and engineering updates",
		"https://example.com/blog", items)
	require.NoError(t, err)
	assert.Contains(t, feed, `<rss version="2.0">`)
	assert.Contains(t, feed, "<title>GoGoGadget Blog</title>")
	assert.Contains(t, feed, "<link>https://example.com/blog/one</link>",
		"an item link must be absolute or no reader can follow it")
	assert.NotContains(t, feed, "Draft Post", "an unpublished entry never reaches the feed")
}
