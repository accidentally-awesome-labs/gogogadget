package content

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var docsSearchFS = fstest.MapFS{
	"docs/billing.md": &fstest.MapFile{Data: []byte(`---
title: Billing
description: Polar subscriptions, checkout, and entitlements.
section: Basics
weight: 10
---

## Webhooks

Polar signs webhook payloads. Entitlements derive from the subscription row.
`)},
	"docs/webhooks.md": &fstest.MapFile{Data: []byte(`---
title: Webhooks
description: Outbound and inbound webhooks.
section: Basics
weight: 20
---

Customer endpoints subscribe to org events. Polar delivery retries with
backoff; inbound webhooks are verified with the webhook-timestamp headers.
`)},
	"docs/storage.md": &fstest.MapFile{Data: []byte(`---
title: Storage
description: R2 uploads behind the Store seam.
section: Platform
weight: 30
---

Uploads are org-scoped and metered against the plan quota.
`)},
}

func searchDocs(t *testing.T) *Docs {
	t.Helper()
	d, err := LoadDocs(docsSearchFS, false)
	require.NoError(t, err)
	return d
}

func TestSearchTitleHitOutranksBodyHit(t *testing.T) {
	d := searchDocs(t)
	results := d.Search("webhooks")
	require.Len(t, results, 2)
	assert.Equal(t, "Webhooks", results[0].Title, "title hit beats body-only hit")
	assert.Equal(t, "Billing", results[1].Title)
	assert.Contains(t, results[1].Snippet, "webhook", "body-only hit carries a snippet around the term")
}

func TestSearchANDSemantics(t *testing.T) {
	d := searchDocs(t)
	// "webhooks backoff": both live only on the webhooks page.
	results := d.Search("webhooks backoff")
	require.Len(t, results, 1)
	assert.Equal(t, "Webhooks", results[0].Title)

	// One term missing everywhere → no results at all.
	assert.Empty(t, d.Search("webhooks kubernetes"))
}

func TestSearchCaseAndMarkupInsensitivity(t *testing.T) {
	d := searchDocs(t)
	results := d.Search("WEBHOOKS") // case-insensitive
	require.Len(t, results, 2)

	// Markdown syntax around a term does not hide it.
	assert.Len(t, d.Search("backoff"), 1)
}

func TestSearchSnippetIsCleanedAndWindowed(t *testing.T) {
	d := searchDocs(t)
	results := d.Search("backoff")
	require.Len(t, results, 1)
	assert.NotContains(t, results[0].Snippet, "##", "heading markers stripped")
	assert.Contains(t, results[0].Snippet, "backoff")
}

func TestSearchEmptyAndShortQueries(t *testing.T) {
	d := searchDocs(t)
	assert.Empty(t, d.Search(""))
	assert.Empty(t, d.Search("   "))
	assert.Empty(t, d.Search("****"))
}
