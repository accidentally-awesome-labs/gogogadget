package ui

import (
	"github.com/a-h/templ"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every exported renderer takes exactly one options struct named after it, and
// that struct embeds Attrs as a field called Attrs. The uniformity is the point:
// a caller never has to remember an argument order, adding an option is not a
// signature change for every existing call site, and Attrs is the single place
// component-owned semantics are protected from callers.
func TestEveryExportedRendererTakesOneOptionsStruct(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_templ.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	checked := 0
	for _, path := range files {
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			// A renderer is a function returning templ.Component.
			if !returnsTemplComponent(fn) {
				continue
			}
			checked++
			name := fn.Name.Name
			params := fn.Type.Params.List
			require.Len(t, params, 1,
				"%s must take exactly one options struct, not %d parameters", name, len(params))
			require.Len(t, params[0].Names, 1, "%s must take a single named parameter", name)

			ident, ok := params[0].Type.(*ast.Ident)
			require.True(t, ok, "%s takes %T rather than a named options struct", name, params[0].Type)
			assert.Equal(t, name+"Opts", ident.Name,
				"%s must take %sOpts so the option type is discoverable from the renderer name", name, name)
		}
	}
	require.Greater(t, checked, 20, "the scan found suspiciously few renderers")
}

// Attrs deliberately has no arbitrary attribute map: with one, any caller could
// set role, aria-*, tabindex or type and silently change what a component means
// to assistive technology.
func TestAttrsHasNoArbitraryAttributeEscapeHatch(t *testing.T) {
	typ := reflect.TypeOf(Attrs{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Type.Kind() != reflect.Map {
			continue
		}
		assert.Contains(t, []string{"Data"}, field.Name,
			"Attrs.%s is a map that could carry arbitrary attributes", field.Name)
	}
	for _, forbidden := range []string{"Attributes", "Extra", "Attrs", "Role", "AriaLabel", "TabIndex", "Type"} {
		_, found := typ.FieldByName(forbidden)
		assert.False(t, found, "Attrs must not expose %s: components own their semantics", forbidden)
	}
}

// An unset or unrecognised Kind renders neutral rather than an uncoloured
// element, and never silently reads as the brand default.
func TestNormalizeKindClosesTheEnum(t *testing.T) {
	for _, kind := range Kinds {
		assert.Equal(t, kind, NormalizeKind(kind))
		assert.True(t, kind.Valid())
	}
	for _, bogus := range []Kind{"", "primary", "Danger", "info "} {
		assert.Equal(t, KindNeutral, NormalizeKind(bogus), "%q must normalize to neutral", bogus)
		assert.False(t, bogus.Valid())
	}
}

func returnsTemplComponent(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	sel, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "templ" && sel.Sel.Name == "Component"
}

// A modal must always be dismissible without JavaScript. Both alert-dialog
// buttons submit the enclosing form method="dialog", which is what closes the
// dialog and records the choice - they were once type="button" with no handler
// at all, trapping the user inside a destructive confirmation.
func TestAlertDialogIsDismissibleWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, AlertDialog(AlertDialogOpts{
		ID: "confirm-delete", Title: "Delete project?", Message: "This cannot be undone.",
		ConfirmLabel: "Delete", CancelLabel: "Keep it", Kind: KindDanger,
	}))

	require.Contains(t, html, `<form method="dialog"`,
		"without a dialog-method form neither button can close the modal")
	assert.Equal(t, 2, strings.Count(html, `type="submit"`),
		"both choices must submit the form: a type=button with no handler is inert")
	assert.Contains(t, html, `value="cancel"`)
	assert.Contains(t, html, `value="confirm"`)
	assert.NotContains(t, html, `type="button"`,
		"a button that closes a modal must not depend on a script being loaded")

	// The consequence, not just the title, has to be announced.
	assert.Contains(t, html, `role="alertdialog"`)
	assert.Contains(t, html, `aria-labelledby="confirm-delete-title"`)
	assert.Contains(t, html, `aria-describedby="confirm-delete-message"`)
	assert.Contains(t, html, `id="confirm-delete-message"`)
}

// An unset kind must still produce a real colour class: "text--text" matches no
// rule and renders an uncoloured heading in a destructive confirmation.
func TestAlertDialogNormalizesItsKind(t *testing.T) {
	html := renderComponent(t, AlertDialog(AlertDialogOpts{ID: "d", Title: "T", Message: "M"}))
	assert.NotContains(t, html, "text--text")
	assert.Contains(t, html, "text-neutral-text")
}

// A declared TestID must reach the DOM, and a declared Class must reach the
// component's ROOT element. Every renderer accepts Attrs, so a renderer that
// ignores them looks configurable while silently dropping the caller's class and
// test id - which is how Tooltip shipped with an Attrs field nothing read.
//
// This is the only Attrs contract test. There used to be a second one over a
// hand-kept list of 44 options structs, guarded by a test whose comment claimed
// the list was tied to the package and which only checked that the list was
// non-empty, free of duplicates, and full of names ending in "Opts" - so three
// quarters of the package was exempt and nothing said so. Reflection over the
// AST-checked renderers table covers all of them, including the field's type.
func TestEveryRendererPropagatesItsAttrs(t *testing.T) {
	table := renderers()
	// The AST scan is the authority: a renderer missing from the table below
	// would otherwise be silently exempt from this contract.
	for _, name := range exportedRendererNames(t) {
		require.Contains(t, table, name,
			"renderer %s is not in the renderers() table, so nothing checks it", name)
	}

	for name, raw := range table {
		fn := reflect.ValueOf(raw)
		t.Run(name, func(t *testing.T) {
			fnType := fn.Type()
			require.Equal(t, 1, fnType.NumIn(), "renderer must take exactly one options struct")
			opts := reflect.New(fnType.In(0)).Elem()
			// Some renderers correctly render nothing for a zero value: a
			// single-page pager and an unnamed icon are both "no output" by
			// design, so they need the minimum input that makes them render.
			if seed, ok := rendererSeeds()[name]; ok {
				opts.Set(reflect.ValueOf(seed))
			}
			attrs := opts.FieldByName("Attrs")
			require.True(t, attrs.IsValid(), "%sOpts has no Attrs field", name)
			require.Equal(t, reflect.TypeOf(Attrs{}), attrs.Type(),
				"%sOpts.Attrs must be ui.Attrs, or callers set id/class/test-id/Alpine/HTMX differently here", name)
			attrs.FieldByName("TestID").SetString("probe-id")
			attrs.FieldByName("Class").SetString("probe-class")

			out := fn.Call([]reflect.Value{opts})
			component, ok := out[0].Interface().(templ.Component)
			require.True(t, ok, "renderer must return a templ.Component")

			html := renderComponent(t, component)
			assert.Contains(t, html, `data-testid="probe-id"`,
				"%s ignores Attrs.TestID, so no test can target it", name)
			// On the element the caller actually addressed, in a class
			// attribute. Searching the whole document passes when the string
			// merely appears somewhere - inside a data-* value, or as text -
			// which is not the contract. TestID and Class come from one Attrs
			// and are flattened into one attribute map, so they land on the same
			// element by construction: asserting them together is what makes the
			// class assertion mean "reached the element" rather than "reached
			// the output".
			assert.Contains(t, classOfProbedElement(t, html), "probe-class",
				"%s drops Attrs.Class from the element that carries Attrs.TestID", name)
		})
	}
}

// classOfProbedElement returns the class attribute of the element carrying the
// probe test id, and refuses an element carrying two.
//
// Two class attributes on one tag is invalid HTML and the browser keeps the
// FIRST, so a component that writes its own class beside a spread map loses its
// own styling to any caller who sets Attrs.Class - which is exactly what
// Checkbox and Switch did to their `h-4 w-4`. The failure is invisible in a
// substring search over the whole document, because both strings are present.
func classOfProbedElement(t *testing.T, html string) string {
	t.Helper()
	at := strings.Index(html, `data-testid="probe-id"`)
	require.GreaterOrEqual(t, at, 0, "the probe test id is not in the output")
	open := strings.LastIndex(html[:at], "<")
	require.GreaterOrEqual(t, open, 0, "the probe test id is not inside a tag")
	end := strings.Index(html[open:], ">")
	require.Greater(t, end, 0, "unterminated tag around the probe test id")
	tag := html[open : open+end]
	matches := probedClassAttr.FindAllStringSubmatch(tag, -1)
	require.Len(t, matches, 1,
		"probed element must carry exactly one class attribute, got %d: %s", len(matches), tag)
	return matches[0][1]
}

var probedClassAttr = regexp.MustCompile(`\sclass="([^"]*)"`)

// rendererSeeds supplies the minimum options for renderers whose zero value
// legitimately produces no output.
func rendererSeeds() map[string]any {
	return map[string]any{
		"Pagination": PaginationOpts{Page: 1, TotalPages: 3, BaseURL: "/x", Target: "#t"},
		"Icon":       IconOpts{Name: IconLogo},
		// Both ends of a keyset sequence render nothing, which is the point.
		"CursorPagination": CursorPaginationOpts{NextURL: "/x?after=1", Target: "#t"},
		// An empty selection has no bulk actions to offer.
		"SelectionBar": SelectionBarOpts{Count: 2, CountLabel: "2 selected"},
		// No request context in a unit render, so the field has no token to
		// draw and correctly renders nothing.
		"CSRFField": CSRFFieldOpts{Token: "probe-token"},
	}
}

// renderers maps every exported renderer to its function value so reflection
// can exercise all of them. TestEveryRendererPropagatesItsAttrs checks this
// table against the AST scan, so it cannot fall behind the package.
func renderers() map[string]any {
	return map[string]any{
		"AlertDialog": AlertDialog, "Badge": Badge, "Banner": Banner, "Card": Card,
		"CardFooter": CardFooter, "CardHeader": CardHeader, "Checkbox": Checkbox, "Container": Container,
		"CSRFField":       CSRFField,
		"DescriptionList": DescriptionList, "Dialog": Dialog, "Separator": Separator, "DropdownMenu": DropdownMenu,
		"EmptyState": EmptyState, "Field": Field, "FieldError": FieldError, "Fieldset": Fieldset,
		"Form": Form, "Grid": Grid, "Icon": Icon, "Inline": Inline,
		"Item": Item, "KeyValue": KeyValue, "List": List, "Meter": Meter,
		"Metric": Metric, "NavTabs": NavTabs, "Notice": Notice, "PageHeader": PageHeader,
		"Pagination": Pagination, "PlanCard": PlanCard, "Popover": Popover, "RadioGroup": RadioGroup,
		"SearchInput": SearchInput, "SecretReveal": SecretReveal, "SectionHeader": SectionHeader, "Select": Select,
		"Spinner": Spinner, "Stack": Stack, "Switch": Switch, "Table": Table,
		"TableCard": TableCard, "TerminalPage": TerminalPage, "TextInput": TextInput,
		"Textarea": Textarea, "Tooltip": Tooltip,
		"Button": Button, "ButtonLink": ButtonLink, "IconButton": IconButton, "Link": Link, "VisuallyHidden": VisuallyHidden, "Heading": Heading, "Text": Text, "Code": Code, "Kbd": Kbd, "Avatar": Avatar, "AvatarGroup": AvatarGroup, "Prose": Prose, "Truncate": Truncate, "ToggleButton": ToggleButton, "ToggleGroup": ToggleGroup, "ButtonGroup": ButtonGroup, "CopyButton": CopyButton,
		"CharCounter": CharCounter, "CheckboxGroup": CheckboxGroup, "ColorInput": ColorInput, "Combobox": Combobox, "DateField": DateField, "DateRangeField": DateRangeField, "DateTimeField": DateTimeField, "FileDropzone": FileDropzone, "FileInput": FileInput, "FormActions": FormActions, "Hint": Hint, "InputAddon": InputAddon, "InputGroup": InputGroup, "Label": Label, "MultiSelect": MultiSelect, "NumberInput": NumberInput, "OTPInput": OTPInput, "PasswordInput": PasswordInput, "RangeInput": RangeInput, "SlugInput": SlugInput, "TagsInput": TagsInput, "TimeField": TimeField,
		"Accordion": Accordion, "BackLink": BackLink, "Breadcrumbs": Breadcrumbs, "Collapsible": Collapsible, "CursorPagination": CursorPagination, "Disclosure": Disclosure, "Menubar": Menubar, "NavigationMenu": NavigationMenu, "SkipLink": SkipLink, "Steps": Steps, "TabPanels": TabPanels, "TableOfContents": TableOfContents,
		"ErrorState": ErrorState, "ProgressBar": ProgressBar, "ProgressCircle": ProgressCircle, "Skeleton": Skeleton, "StatusDot": StatusDot, "Toast": Toast,
		"ConfirmAction": ConfirmAction, "ContextMenu": ContextMenu, "Drawer": Drawer, "HoverCard": HoverCard,
		"AspectRatio": AspectRatio, "Attachment": Attachment, "Center": Center, "ColumnHeader": ColumnHeader, "DataTable": DataTable, "RowActions": RowActions, "ScrollArea": ScrollArea, "Section": Section, "SelectionBar": SelectionBar, "Split": Split, "StatGroup": StatGroup, "StickyBar": StickyBar, "TableToolbar": TableToolbar, "Tile": Tile, "Toolbar": Toolbar,
		"NotificationItem": NotificationItem, "ActivityItem": ActivityItem, "Timeline": Timeline, "Comment": Comment, "CommentThread": CommentThread, "Composer": Composer, "ChatMessage": ChatMessage, "ChatLog": ChatLog, "MentionChip": MentionChip, "DeliveryStatus": DeliveryStatus, "UsageCard": UsageCard, "OnboardingChecklist": OnboardingChecklist, "SettingsSection": SettingsSection, "MemberItem": MemberItem,
		"BarChart": BarChart, "LineChart": LineChart, "AreaChart": AreaChart, "DonutChart": DonutChart, "Sparkline": Sparkline, "ChartLegend": ChartLegend,
		"MonthGrid": MonthGrid, "DatePicker": DatePicker, "DateTimePicker": DateTimePicker, "DateRangePicker": DateRangePicker, "Scheduler": Scheduler, "AvailabilityGrid": AvailabilityGrid,
		"MarkdownEditor": MarkdownEditor, "EditorToolbar": EditorToolbar, "EditorPreview": EditorPreview, "MediaPicker": MediaPicker,
		"DataGrid": DataGrid, "GridToolbar": GridToolbar, "ColumnPicker": ColumnPicker,
		"Kanban": Kanban, "KanbanColumn": KanbanColumn, "KanbanCard": KanbanCard,
		"Tree": Tree, "TreeNode": TreeNode, "TreeGrid": TreeGrid, "Carousel": Carousel, "Slide": Slide, "CarouselDots": CarouselDots, "CommandPalette": CommandPalette, "CommandGroup": CommandGroup, "CommandItem": CommandItem, "PanelGroup": PanelGroup, "Panel": Panel, "PanelHandle": PanelHandle, "Questionnaire": Questionnaire, "Question": Question, "WizardActions": WizardActions,
	}
}

// exportedRendererNames returns every exported function in the generated templ
// output that returns a templ.Component.
func exportedRendererNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_templ.go")
	require.NoError(t, err)
	var out []string
	for _, path := range files {
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, parseErr)
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !returnsTemplComponent(fn) {
				continue
			}
			out = append(out, fn.Name.Name)
		}
	}
	require.NotEmpty(t, out)
	return out
}

// PagerLabels holds functions, so an omitted label used to nil-dereference and
// take down the whole page render. A missing translation must degrade to a
// readable fallback instead: an English arrow is a translation bug, a panic is
// an outage.
func TestPaginationSurvivesMissingLabels(t *testing.T) {
	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/app/projects", Target: "#table",
	}))

	assert.Contains(t, html, `aria-label="Pagination"`,
		"the landmark still needs a name when no localized one is supplied")
	assert.Contains(t, html, "Previous")
	assert.Contains(t, html, "Next")
	assert.Contains(t, html, "2 / 5")

	// A supplied label must still win over every fallback.
	localized := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/app/projects", Target: "#table",
		Labels: PagerLabels{
			Aria:   func(int, int) string { return "Paginación" },
			Prev:   func(int, int) string { return "Anterior" },
			Next:   func(int, int) string { return "Siguiente" },
			PageOf: func(p, n int) string { return "Página 2 de 5" },
		},
	}))
	assert.Contains(t, localized, `aria-label="Paginación"`)
	assert.Contains(t, localized, "Anterior")
	assert.NotContains(t, localized, "Previous")
}

// MenuItem and Column are data contracts shared by several renderers, so their
// fields are fixed here rather than per consumer: DropdownMenu, ContextMenu,
// RowActions and Kanban all read MenuItem, and Table, DataTable, DataGrid and
// TreeGrid all read Column. A consumer that needed its own field would fork the
// contract and break the others.
func TestSharedDataContractShapes(t *testing.T) {
	menu := reflect.TypeOf(MenuItem{})
	for name, want := range map[string]reflect.Kind{
		"Label": reflect.String, "Href": reflect.String,
		"Icon": reflect.String, "Kind": reflect.String,
		"Disabled": reflect.Bool, "Separator": reflect.Bool,
		"Confirm": reflect.String,
	} {
		field, ok := menu.FieldByName(name)
		require.True(t, ok, "MenuItem has no %s field", name)
		assert.Equal(t, want, field.Type.Kind(), "MenuItem.%s has the wrong kind", name)
	}
	iconField, _ := menu.FieldByName("Icon")
	assert.Equal(t, reflect.TypeOf(IconName("")), iconField.Type,
		"MenuItem.Icon must be the typed icon name, not a free string")
	hx, ok := menu.FieldByName("HX")
	require.True(t, ok, "a menu item that issues a request needs HX")
	assert.Equal(t, reflect.TypeOf(HX{}), hx.Type)

	col := reflect.TypeOf(Column{})
	hide, ok := col.FieldByName("HideBelow")
	require.True(t, ok, "a column that cannot be hidden forces horizontal scroll on small screens")
	assert.Equal(t, reflect.TypeOf(Breakpoint("")), hide.Type)
	align, _ := col.FieldByName("Align")
	assert.Equal(t, reflect.TypeOf(Align("")), align.Type)

	width, ok := col.FieldByName("Width")
	require.True(t, ok, "a column with no width cannot stop a timestamp column wrapping")
	assert.Equal(t, reflect.String, width.Type.Kind(),
		"Width is a CSS length, so it is a string rather than a pixel count")
	// A declared field is only a contract if a renderer honours it. Width was
	// declared, documented and populated while every renderer dropped it, so the
	// shape check alone is what let that ship.
	for name, html := range map[string]string{
		"ColumnHeader": renderComponent(t, ColumnHeader(ColumnHeaderOpts{
			Column: Column{Key: "when", Label: "When", Width: "12rem"},
		})),
		"TreeGrid": renderComponent(t, TreeGrid(TreeGridOpts{
			ID: "effort", Label: "Effort", Columns: []Column{{Key: "when", Label: "When", Width: "12rem"}},
		})),
	} {
		// The trailing quote is deliberately not asserted: templ's style
		// attribute expression appends a semicolon and the attribute map does
		// not, so the two renderers differ by one character after the value.
		assert.Contains(t, html, `style="width:12rem`, "%s drops Column.Width", name)
	}
	// The width is a length, not an inline-style hook: a declaration list would
	// let a caller reach past every rule the design system enforces on classes.
	bogus := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "k", Label: "K", Width: "8rem;position:fixed"},
	}))
	assert.NotContains(t, bogus, "position:fixed",
		"Width must carry a bare length, never a declaration list")
}

// A separator is not a command: it carries no label, no href and no handler, so
// rendering it as an item would put an empty, focusable row in the menu.
//
// This test used to spend its last assertion on hx-confirm - on an item with an
// href and no request, where the attribute gates nothing - and asserted nothing
// about the separator beyond the role. What a divider must be is the subject
// here: an <hr> with the separator role, no accessible name, and no tab stop.
func TestMenuSeparatorRendersAsSeparator(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{
			{Label: "Rename", Href: "/x"},
			{Separator: true},
			{Label: "Delete", Href: "/y", Kind: KindDanger},
		},
	}))

	assert.Contains(t, html, `<hr role="separator"`,
		"a divider is an hr: a div with the role is a line assistive technology reads as content")
	assert.Equal(t, 1, strings.Count(html, `role="separator"`),
		"one separator in, one separator out")
	assert.Equal(t, 2, strings.Count(html, "<a "),
		"a separator must not render as a link")
	assert.Equal(t, 2, strings.Count(html, `class="block px-3 py-2 text-sm`),
		"a separator must not take an item's padding, hover or text styling")
	// The two commands survive around it, in order, so a separator between them
	// is a divider rather than a truncation point.
	assert.Less(t, strings.Index(html, "Rename"), strings.Index(html, `role="separator"`))
	assert.Less(t, strings.Index(html, `role="separator"`), strings.Index(html, "Delete"))
}

// hx-confirm gates an htmx request and nothing else. On a plain link or an inert
// button htmx never processes the activation, so the attribute promises a prompt
// that never appears and an action that never runs - which is what the
// Operations scenario shipped. The component refuses the combination.
func TestMenuConfirmOnlyRidesARealRequest(t *testing.T) {
	acting := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{{Label: "Delete", Kind: KindDanger, Confirm: "Delete this?", HX: HX{Delete: "/x"}}},
	}))
	assert.Contains(t, acting, `hx-confirm="Delete this?"`,
		"an item that issues a request keeps its declared confirmation")

	for name, item := range map[string]MenuItem{
		"navigating": {Label: "Delete", Href: "/y", Confirm: "Delete this?"},
		"inert":      {Label: "Cancel", Confirm: "Cancel this?"},
		// Target and swap modify a request some other attribute has to declare.
		"modifiers only": {Label: "Cancel", Confirm: "Cancel this?", HX: HX{Target: "#t", Swap: "outerHTML"}},
	} {
		html := renderComponent(t, DropdownMenu(DropdownMenuOpts{Label: "Actions", Items: []MenuItem{item}}))
		assert.NotContains(t, html, "hx-confirm",
			"a %s item cannot show a confirmation, so it must not claim one", name)
	}

	// A boosted link is the exception: htmx handles its navigation, so the
	// prompt does gate something.
	boosted := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{{Label: "Leave", Href: "/y", Confirm: "Discard changes?", HX: HX{Boost: true}}},
	}))
	assert.Contains(t, boosted, `hx-confirm="Discard changes?"`)
}

// A menu item that acts is a button; one that navigates is a link. Rendering an
// acting item as <a href=""> makes it a link to the current page, so a click
// before HTMX has loaded reloads the page instead of doing nothing.
func TestActingMenuItemIsAButtonNotAnEmptyLink(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{
			{Label: "Open", Href: "/projects/1"},
			{Label: "Delete", Kind: KindDanger, Confirm: "Sure?", HX: HX{Delete: "/projects/1"}},
		},
	}))

	assert.NotContains(t, html, `href=""`,
		"an empty href is a link to the current page")
	assert.Contains(t, html, `href="/projects/1"`, "a navigating item stays a link")
	assert.Contains(t, html, `<button type="button"`, "an acting item must be a button")
	assert.Contains(t, html, `hx-delete="/projects/1"`)
}

// A form whose submission the CSRF check applies to must carry the token, and
// the only way a component can know is to render CSRFField. Production templates
// are covered by the design-system guard, but ui/ is where forms are legitimately
// built, so nothing watched these three renderers - and two of them shipped the
// exact defect that guard exists to prevent: Composer submitted as a GET to the
// current URL because its verb lived in an htmx attribute, and Questionnaire
// declared method="post" with no field, so a plain submit answered 403.
//
// Scanning source rather than rendering is deliberate: a rendered form only
// carries the token when a token is in the context, so a rendering test would
// pass on an empty context for the wrong reason.
func TestEveryUnsafeFormInThePackageRendersTheToken(t *testing.T) {
	entries, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no templ sources found, so this test proves nothing")
	}

	// form.templ renders the field through formCarriesCSRF, and alert-dialog's
	// form is method="dialog" - a platform close, never a request. Both are
	// stated rather than pattern-matched, because each is a real decision.
	exempt := map[string]string{
		"form.templ":         "renders CSRFField itself, gated on the derived method",
		"alert-dialog.templ": `method="dialog" closes the dialog and issues no request`,
	}

	for _, path := range entries {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Comment lines are stripped first. markdown-editor.templ documents the
		// caller's obligation by showing the <form id=... action=...> the page
		// must render, and a scan that counted prose would have reported a
		// component with no form element at all as missing its token.
		source := withoutCommentLines(string(body))
		if !strings.Contains(source, "<form") {
			continue
		}
		if reason, ok := exempt[filepath.Base(path)]; ok {
			assert.NotEmpty(t, reason)
			continue
		}

		// A form is unsafe if it declares a mutating method or carries a
		// mutating htmx verb. Either one means a browser submit gets checked.
		unsafe := strings.Contains(source, `method="post"`) ||
			strings.Contains(source, "composerHX") ||
			strings.Contains(source, "hx-post") ||
			strings.Contains(source, "hx-put") ||
			strings.Contains(source, "hx-patch") ||
			strings.Contains(source, "hx-delete") ||
			strings.Contains(source, "HX.Post") ||
			strings.Contains(source, "hxMutatingURL")
		if !unsafe {
			continue
		}

		assert.Containsf(t, source, "CSRFField",
			"%s renders a form whose submission is checked but never renders CSRFField, so its no-script path answers 403", path)
	}
}

// withoutCommentLines drops whole-line comments so a scan reads markup rather
// than prose. Anything a comment says about a form is documentation, not an
// element the browser will ever submit.
func withoutCommentLines(source string) string {
	lines := strings.Split(source, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
