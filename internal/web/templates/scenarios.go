package templates

import "github.com/a-h/templ"

// Scenario describes one realistic product surface the dev catalog can render.
//
// The list is declared rather than discovered because a scenario is a
// deliberate composition: which components appear together, in which layout,
// under which states. Discovering it from installed modules would produce a
// component index, which the gallery already is.
type Scenario struct {
	// Slug is the URL segment and the identity used in the visual surface
	// matrix, so renaming one is a visible change rather than a silent
	// baseline reset.
	Slug string
	// Title and Summary describe the surface a reader is looking at.
	Title   string
	Summary string
	// Layout is the shell this scenario renders inside: the real
	// PublicLayout/AppLayout/AdminLayout, never a copy of their markup. It is
	// the same string Page.Layout takes, so a scenario cannot name a shell the
	// renderer does not have.
	Layout string
	// Surfaces names the components this scenario is built to exercise. It is
	// the reason the scenario exists, and it is what the visual matrix and the
	// accessibility sweep read to know what they are covering.
	Surfaces []string
	// States are the query values this scenario accepts. A state absent here is
	// refused rather than silently rendering the default, because a URL that
	// looks like it selected something and did not is worse than a 404.
	States []string
	// Render composes the surface. It renders inside #content only: the shell
	// around it comes from the real PublicLayout/AppLayout/AdminLayout, so a
	// scenario cannot drift from the chrome the product actually ships.
	//
	// Nil means the scenario is declared but has no composition yet, which the
	// page states rather than rendering an empty column.
	Render func(GalleryContext) templ.Component
}

// ScenarioRegistry is every dev scenario, in the order the catalog presents
// them: the surfaces a product actually has, roughly in the order a team builds
// them.
var ScenarioRegistry = []Scenario{
	{
		Slug: "dashboard", Title: "Dashboard", Layout: LayoutApp,
		Summary:  "First-run onboarding, headline metrics, a command trigger and recent activity.",
		Surfaces: []string{"onboarding-checklist", "stat-group", "metric", "command-palette", "activity-item"},
		States:   []string{"default", "empty", "loading"},
		Render:   ScenarioDashboard,
	},
	{
		Slug: "resource-list", Title: "Resource list", Layout: LayoutApp,
		Summary:  "A table with search, filter, sort, selection, bulk actions and pagination.",
		Surfaces: []string{"data-table", "search-input", "column-header", "selection-bar", "row-actions", "pagination", "empty-state"},
		States:   []string{"default", "empty", "loading", "error"},
		Render:   ScenarioResourceList,
	},
	{
		Slug: "settings", Title: "Settings", Layout: LayoutApp,
		Summary:  "Account, organisation and security forms with validation, saving feedback and a danger zone.",
		Surfaces: []string{"nav-tabs", "settings-section", "description-list", "field", "text-input", "alert-dialog"},
		States:   []string{"default", "error", "success", "readonly"},
		Render:   ScenarioSettings,
	},
	{
		Slug: "team", Title: "Team", Layout: LayoutApp,
		Summary:  "Members, invitations and roles, including the read-only view a non-admin sees.",
		Surfaces: []string{"avatar", "avatar-group", "member-item", "data-table", "badge", "dropdown-menu"},
		States:   []string{"default", "empty", "readonly"},
		Render:   ScenarioTeam,
	},
	{
		Slug: "billing", Title: "Billing", Layout: LayoutApp,
		Summary:  "Plans, quotas, invoices, dunning notices and processing states.",
		Surfaces: []string{"plan-card", "usage-card", "meter", "progress-bar", "notice", "banner", "data-table"},
		States:   []string{"default", "loading", "error", "success"},
		Render:   ScenarioBilling,
	},
	{
		Slug: "developer", Title: "Developer", Layout: LayoutApp,
		Summary:  "API keys, secret reveal, webhook endpoints, code samples and delivery states.",
		Surfaces: []string{"secret-reveal", "code", "copy-button", "data-table", "delivery-status", "status-dot"},
		States:   []string{"default", "empty", "error"},
		Render:   ScenarioDeveloper,
	},
	{
		Slug: "operations", Title: "Operations", Layout: LayoutAdmin,
		Summary:  "Jobs, schedules, feature flags and audit rows behind admin-role gating.",
		Surfaces: []string{"data-table", "badge", "status-dot", "row-actions", "toolbar", "pagination"},
		States:   []string{"default", "empty", "readonly"},
		Render:   ScenarioOperations,
	},
	{
		Slug: "content", Title: "Content", Layout: LayoutAdmin,
		Summary:  "The Markdown editor with media, revisions, server-rendered preview and attachments.",
		Surfaces: []string{"markdown-editor", "editor-toolbar", "editor-preview", "media-picker", "attachment", "panel-group"},
		States:   []string{"default", "error", "success"},
		Render:   ScenarioContent,
	},
	{
		Slug: "planning", Title: "Planning", Layout: LayoutApp,
		Summary:  "Calendar and scheduler, a board, a hierarchy and resizable panels together.",
		Surfaces: []string{"date-picker", "scheduler", "kanban", "tree", "tree-grid", "panel-group"},
		States:   []string{"default", "empty"},
		Render:   ScenarioPlanning,
	},
	{
		Slug: "communication", Title: "Communication", Layout: LayoutApp,
		Summary:  "Notifications, comment threads, a chat log, a composer and attachments.",
		Surfaces: []string{"notification-item", "comment", "comment-thread", "chat-log", "chat-message", "composer", "attachment"},
		States:   []string{"default", "empty", "loading"},
		Render:   ScenarioCommunication,
	},
	{
		Slug: "analytics", Title: "Analytics", Layout: LayoutApp,
		Summary:  "Chart families with date filters, a data grid and the accessible summaries beside them.",
		Surfaces: []string{"bar-chart", "line-chart", "area-chart", "donut-chart", "sparkline", "chart-legend", "date-range-picker", "data-grid"},
		States:   []string{"default", "empty", "loading"},
		Render:   ScenarioAnalytics,
	},
	{
		Slug: "system-states", Title: "System states", Layout: LayoutPublic,
		Summary:  "Skeletons, progress, empty and error states, and every terminal page from 403 to 503.",
		Surfaces: []string{"skeleton", "progress-bar", "progress-circle", "empty-state", "error-state", "terminal-page", "banner"},
		States:   []string{"default", "loading", "error"},
		Render:   ScenarioSystemStates,
	},
}

// ScenarioBySlug returns the declared scenario for a URL segment. An unknown
// slug is not found rather than falling back to the first scenario: a typo that
// silently renders a different page teaches the reader the wrong thing.
// scenarioAxisTestID names an axis option. The state options keep their
// original "state-<value>" ids because tests and the visual matrix already
// reference them.
func scenarioAxisTestID(key, option string) string {
	if key == "state" {
		return "state-" + option
	}
	return key + "-" + option
}

// scenarioContextSummary states what is on screen, so a screenshot carries its
// own axes rather than needing the URL beside it.
func scenarioContextSummary(gc GalleryContext) string {
	content := ContentNormal
	if gc.LongContent {
		content = ContentLong
	}
	return gc.Direction + " · " + content + " content · " + string(gc.Density.Value())
}

func ScenarioBySlug(slug string) (Scenario, bool) {
	for _, scenario := range ScenarioRegistry {
		if scenario.Slug == slug {
			return scenario, true
		}
	}
	return Scenario{}, false
}

// HasState reports whether this scenario accepts a state. Every scenario accepts
// "default", so an absent query parameter is always valid.
func (s Scenario) HasState(state string) bool {
	if state == "" || state == "default" {
		return true
	}
	for _, candidate := range s.States {
		if candidate == state {
			return true
		}
	}
	return false
}
