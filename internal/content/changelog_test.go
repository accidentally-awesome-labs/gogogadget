package content_test

import (
	"strings"
	"testing"
	"time"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shippedReleases parses the seed corpus with the same helper the importer
// uses, so these assertions cover exactly the rows /changelog is seeded from.
func shippedReleases(t *testing.T) []content.Release {
	t.Helper()
	releases, err := content.ParseReleases(contentfs.FS)
	require.NoError(t, err)
	return releases
}

func TestChangelogLoadsNewestFirst(t *testing.T) {
	releases := shippedReleases(t)
	require.NotEmpty(t, releases, "the shipped changelog must not be empty")

	for i := 1; i < len(releases); i++ {
		assert.True(t, releases[i-1].Date.After(releases[i].Date),
			"releases must be strictly newest-first: %s is not after %s",
			releases[i-1].Slug, releases[i].Slug)
	}
}

// Every entry must be complete: an anchor, a title, a date, and a body. A
// release note with a missing field is worse than no note — it reads like the
// release did nothing.
func TestChangelogEntriesAreComplete(t *testing.T) {
	for _, r := range shippedReleases(t) {
		t.Run(r.Slug, func(t *testing.T) {
			assert.NotEmpty(t, r.Title)
			assert.NotEmpty(t, r.Summary, "the summary is what a scanner reads")
			assert.False(t, r.Date.IsZero())
			assert.Contains(t, r.Body, "<", "body must render to HTML")
			// The slug is the anchor and the date must agree with it, or a
			// link like /changelog#2026-08-19 points at the wrong release.
			assert.Equal(t, r.Date.Format("2006-01-02"), r.Slug)
			// The anchor must be a usable CSS selector: ids cannot start
			// with a digit, or querySelector("#2026-08-19") throws.
			assert.Equal(t, "release-"+r.Slug, r.Anchor)
		})
	}
}

func TestChangelogRendersMarkdownSafely(t *testing.T) {
	for _, r := range shippedReleases(t) {
		assert.NotContains(t, r.Body, "<script", "goldmark runs without WithUnsafe: raw HTML stays escaped")
	}
}

// The changelog describes a real product, so its dates have to sit inside the
// project's actual lifetime — a typo like 2027 would silently reorder the page.
func TestChangelogDatesArePlausible(t *testing.T) {
	first := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for _, r := range shippedReleases(t) {
		assert.False(t, r.Date.Before(first), "%s predates the project", r.Slug)
		assert.False(t, r.Date.After(time.Now().AddDate(0, 0, 1)), "%s is in the future", r.Slug)
	}
}

// Entries claim specific shipped features. This pins a handful of load-bearing
// claims so the page cannot drift into marketing that the code does not back.
func TestChangelogClaimsMatchShippedFeatures(t *testing.T) {
	joined := strings.Builder{}
	for _, r := range shippedReleases(t) {
		joined.WriteString(r.Title + " " + r.Summary + " " + r.Body)
	}
	all := joined.String()

	for _, claim := range []string{
		"Idempotency-Key",      // internal/api/idempotency.go
		"OpenAPI",              // internal/api/openapi.yaml
		"AUDIT_RETENTION_DAYS", // internal/config
		"API_RATE_LIMIT_RPM",   // internal/config
		"EventSource",          // static/app.js
		"support",              // users.admin_role
		"dunning",              // internal/billing
		"content_entries",      // internal/db/migrations/0019_content_cms.sql
		"/admin/content",       // internal/web/handlers_admin_content.go
	} {
		assert.Contains(t, strings.ToLower(all), strings.ToLower(claim),
			"the changelog should record %q, which shipped", claim)
	}
}
