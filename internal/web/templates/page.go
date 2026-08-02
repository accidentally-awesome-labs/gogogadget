package templates

import "time"

// Layout names for Page.Layout.
const (
	LayoutPublic = "public"
	LayoutApp    = "app"
	LayoutAdmin  = "admin"
	LayoutDocs   = "docs"
)

// Page is the per-request view model every layout receives. Fields are added
// as capabilities land (identity adds User/Org, billing adds Plan) — layouts
// tolerate zero values.
type Page struct {
	Title       string
	Description string
	Path        string
	Layout      string
	CSRFToken   string
	AppURL      string

	PostHogKey          string
	ClerkPublishableKey string
	ClerkFrontendAPIURL string

	// Now is the render clock (frozen under APP_ENV=test via TEST_NOW) so
	// rendered dates never rot visual baselines.
	Now func() time.Time
}

// NowOrDefault guards against nil Now in tests that construct Page directly.
func (p Page) NowOrDefault() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}
