package templates

import (
	"context"

	"github.com/gogogadget/gogogadget/internal/i18n"
)

// NavItem is one chrome link. LabelKey is an i18n key, not a resolved string:
// these are package-level values and translation needs the request context.
// Match is the path prefix that marks the item current (defaults to Href).
type NavItem struct{ LabelKey, Href, Match string }

// MatchPath is the prefix navCurrent compares the request path against.
func (n NavItem) MatchPath() string {
	if n.Match != "" {
		return n.Match
	}
	return n.Href
}

// NavColumn is one footer column.
type NavColumn struct {
	TitleKey string
	Items    []NavItem
}

// Chrome is the product identity and navigation a rebrand edits. Everything
// here is read at render time, so overriding these values in an init() or at
// startup restyles the shell without touching a template.
//
// Page CONTENT does not belong here — the marketing copy in home.templ stays
// with the page that renders it. This is the frame, not the picture.
var (
	BrandName = "GoGoGadget"

	// DocsEditBase is the prefix of the "edit this page" link on every docs
	// page; a fork points it at its own repository.
	DocsEditBase = "https://github.com/gogogadget/gogogadget/edit/main/content/docs/"

	PublicNav = []NavItem{
		{LabelKey: "nav.features", Href: "/#features"},
		{LabelKey: "nav.pricing", Href: "/pricing"},
		{LabelKey: "nav.blog", Href: "/blog"},
		{LabelKey: "nav.docs", Href: "/docs"},
		{LabelKey: "nav.changelog", Href: "/changelog"},
	}

	AppNav = []NavItem{
		{LabelKey: "sidebar.dashboard", Href: "/app", Match: "/app"},
		{LabelKey: "sidebar.projects", Href: "/app/projects", Match: "/app/projects"},
		{LabelKey: "sidebar.files", Href: "/app/files", Match: "/app/files"},
		{LabelKey: "sidebar.activity", Href: "/app/activity", Match: "/app/activity"},
		{LabelKey: "sidebar.settings", Href: "/app/settings/account", Match: "/app/settings"},
	}

	AdminNav = []NavItem{
		{LabelKey: "sidebar.admin_overview", Href: "/admin", Match: "/admin"},
		{LabelKey: "sidebar.admin_users", Href: "/admin/users", Match: "/admin/users"},
		{LabelKey: "sidebar.admin_orgs", Href: "/admin/orgs", Match: "/admin/orgs"},
		{LabelKey: "sidebar.admin_flags", Href: "/admin/flags", Match: "/admin/flags"},
		{LabelKey: "sidebar.admin_audit", Href: "/admin/audit", Match: "/admin/audit"},
		{LabelKey: "sidebar.admin_jobs", Href: "/admin/jobs", Match: "/admin/jobs"},
		{LabelKey: "sidebar.admin_announcements", Href: "/admin/announcements", Match: "/admin/announcements"},
		{LabelKey: "sidebar.admin_content", Href: "/admin/content", Match: "/admin/content"},
		{LabelKey: "sidebar.admin_schedules", Href: "/admin/schedules", Match: "/admin/schedules"},
	}

	FooterColumns = []NavColumn{
		{TitleKey: "footer.product", Items: []NavItem{
			{LabelKey: "footer.features", Href: "/#features"},
			{LabelKey: "footer.pricing", Href: "/pricing"},
			{LabelKey: "footer.docs", Href: "/docs"},
		}},
		{TitleKey: "footer.company", Items: []NavItem{
			{LabelKey: "footer.blog", Href: "/blog"},
			{LabelKey: "footer.changelog", Href: "/changelog"},
			{LabelKey: "footer.about", Href: "/#about"},
		}},
		{TitleKey: "footer.resources", Items: []NavItem{
			{LabelKey: "footer.getting_started", Href: "/docs/getting-started"},
			{LabelKey: "footer.api", Href: "/docs/api"},
			{LabelKey: "footer.rss", Href: "/rss.xml"},
		}},
		{TitleKey: "footer.legal", Items: []NavItem{
			{LabelKey: "footer.terms", Href: "/terms"},
			{LabelKey: "footer.privacy", Href: "/privacy"},
		}},
	}
)

// NavLabel resolves a NavItem's label in the request locale.
func NavLabel(ctx context.Context, item NavItem) string {
	return i18n.T(ctx, item.LabelKey)
}

// ChromeCatalogKeys is every i18n key the chrome config and the settings tabs
// reference. The template scanner's literal regexp cannot see keys that arrive
// as struct VALUES, so TestChromeKeysExistInCatalogs walks this instead.
func ChromeCatalogKeys() []string {
	keys := []string{"footer.copyright"}
	// A bare context leaves every feature gate at its default, which for the
	// webhooks tab is "shown" — so this covers the whole tab list.
	for _, group := range [][]NavItem{PublicNav, AppNav, AdminNav, settingsTabs(context.Background())} {
		for _, item := range group {
			keys = append(keys, item.LabelKey)
		}
	}
	for _, col := range FooterColumns {
		keys = append(keys, col.TitleKey)
		for _, item := range col.Items {
			keys = append(keys, item.LabelKey)
		}
	}
	return keys
}
