package templates

import (
	"context"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// NavItem is one chrome link. LabelKey is an i18n key, not a resolved string:
// these are package-level values and translation needs the request context.
// Match is the path prefix that marks the item current (defaults to Href).
type NavItem struct {
	// ID is the stable declaration id, which is what another module orders
	// itself against.
	ID                    string
	LabelKey, Href, Match string
}

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

// Chrome is the product identity a rebrand edits. Navigation itself is not
// here: PublicNav, AppNav, AdminNav, FooterColumns, and the settings tabs are
// generated from module declarations (chrome_registry_gen.go), so installing a
// page installs its own entry and removing it takes the entry with it. Hrefs
// come from the route table, so a nav link cannot outlive its route.
//
// Page CONTENT does not belong here either — the marketing copy in home.templ
// stays with the page that renders it. This is the frame, not the picture.
var (
	BrandName = "GoGoGadget"

	// DocsEditBase is the prefix of the "edit this page" link on every docs
	// page; a fork points it at its own repository.
	DocsEditBase = "https://github.com/gogogadget/gogogadget/edit/main/content/docs/"
)

// NavLabel resolves a NavItem's label in the request locale.
func NavLabel(ctx context.Context, item NavItem) string {
	return i18n.T(ctx, item.LabelKey)
}

// settingsTabs is the tabs the current viewer may see. A tab whose page would
// 404 or 403 for them is worse than an absent tab, so the declared role and
// flag conditions are evaluated per request rather than baked in.
func settingsTabs(ctx context.Context) []NavItem {
	items := make([]NavItem, 0, len(SettingsNavigationRegistry))
	for _, tab := range SettingsNavigationRegistry {
		if navConditionsMet(ctx, tab.Roles, tab.Flags) {
			items = append(items, tab.Item)
		}
	}
	return items
}

// navConditionsMet evaluates a declared entry's conditions against the current
// request. An unrecognised condition name is treated as unmet: showing a gated
// entry because of a typo is the worse failure, and generation refuses unknown
// names anyway, so this is a second line rather than the only one.
func navConditionsMet(ctx context.Context, roles, flags []string) bool {
	for _, flag := range flags {
		if flag != navFlagWebhooks || !WebhooksEnabled(ctx) {
			return false
		}
	}
	user := identity.UserFrom(ctx)
	for _, role := range roles {
		switch role {
		case navRoleStaff:
			if !identity.IsStaff(user) {
				return false
			}
		case navRoleAdmin:
			if !identity.IsAdmin(user) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// The condition names a manifest may use. Generation validates declarations
// against these same names, so a typo is caught before it can hide a tab.
const (
	navFlagWebhooks = "webhooks"
	navRoleStaff    = "staff"
	navRoleAdmin    = "admin"
)

// ChromeCatalogKeys is every i18n key the generated chrome references. The
// template scanner's literal regexp cannot see keys that arrive as struct
// VALUES, so TestChromeKeysExistInCatalogs walks this instead.
func ChromeCatalogKeys() []string {
	keys := []string{"footer.copyright"}
	for _, group := range [][]NavItem{PublicNav, AppNav, AdminNav} {
		for _, item := range group {
			keys = append(keys, item.LabelKey)
		}
	}
	// Every declared tab, not just the ones the current viewer sees: a gated
	// tab still needs its label translated.
	for _, tab := range SettingsNavigationRegistry {
		keys = append(keys, tab.Item.LabelKey)
	}
	for _, col := range FooterColumns {
		keys = append(keys, col.TitleKey)
		for _, item := range col.Items {
			keys = append(keys, item.LabelKey)
		}
	}
	return keys
}
