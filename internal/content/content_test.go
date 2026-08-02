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

func TestLoadBlog(t *testing.T) {
	blog, err := LoadBlog(testFS, false)
	require.NoError(t, err)
	require.Len(t, blog.Posts, 3)

	// Sorted date-desc.
	assert.Equal(t, "Draft Post", blog.Posts[0].Title)
	assert.Equal(t, "First Post", blog.Posts[2].Title)

	// Markdown rendered; raw HTML escaped (never html.WithUnsafe).
	first := blog.BySlug("one")
	require.NotNil(t, first)
	assert.Contains(t, first.Body, "<h1>Heading</h1>")
	assert.Contains(t, first.Body, "<strong>bold</strong>")
	assert.NotContains(t, first.Body, "<script>")
	assert.Contains(t, first.Body, "raw HTML omitted")
}

func TestLoadBlogDraftsExcludedInProduction(t *testing.T) {
	blog, err := LoadBlog(testFS, true)
	require.NoError(t, err)
	assert.Len(t, blog.Posts, 2)
	assert.Nil(t, blog.BySlug("draft"))
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

func TestRSS(t *testing.T) {
	blog, err := LoadBlog(testFS, false)
	require.NoError(t, err)
	feed, err := RSS("https://example.com", blog.Posts)
	require.NoError(t, err)
	assert.Contains(t, feed, `<rss version="2.0">`)
	assert.Contains(t, feed, "<link>https://example.com/blog/one</link>")
	assert.NotContains(t, feed, "Draft Post", "drafts never reach the feed")
}
