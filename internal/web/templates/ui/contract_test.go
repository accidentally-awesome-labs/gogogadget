package ui

import (
	"github.com/a-h/templ"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
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

// Attrs must be reachable as a field called Attrs on every options struct, so
// callers set id/class/test-id/Alpine/HTMX the same way everywhere.
func TestEveryOptionsStructEmbedsAttrs(t *testing.T) {
	for _, sample := range optionsSamples() {
		typ := reflect.TypeOf(sample)
		field, ok := typ.FieldByName("Attrs")
		require.True(t, ok, "%s has no Attrs field", typ.Name())
		assert.Equal(t, reflect.TypeOf(Attrs{}), field.Type,
			"%s.Attrs must be ui.Attrs", typ.Name())
	}
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

// optionsSamples lists one zero value per options struct. Kept explicit rather
// than reflected out of the package so a new renderer's options struct is a
// deliberate addition here.
func optionsSamples() []any {
	return []any{
		SpinnerOpts{}, BadgeOpts{}, NoticeOpts{}, BannerOpts{}, PageHeaderOpts{},
		SectionHeaderOpts{}, TableCardOpts{}, MetricOpts{},
		EmptyStateOpts{}, FieldErrorOpts{}, MeterOpts{}, SecretRevealOpts{},
		DescriptionListOpts{}, PaginationOpts{}, NavTabsOpts{}, TerminalPageOpts{},
		FormOpts{}, FieldsetOpts{}, FieldOpts{}, TextInputOpts{}, SearchInputOpts{},
		TextareaOpts{}, SelectOpts{}, CheckboxOpts{}, RadioGroupOpts{}, SwitchOpts{},
		CardOpts{}, CardHeaderOpts{}, CardFooterOpts{}, TableOpts{}, KeyValueOpts{},
		ListOpts{}, ContainerOpts{}, StackOpts{}, InlineOpts{}, GridOpts{},
		DialogOpts{}, AlertDialogOpts{}, DropdownMenuOpts{}, PopoverOpts{}, TooltipOpts{},
		IconOpts{}, ItemOpts{}, SeparatorOpts{},
	}
}

func TestOptionsSamplesCoverEveryRenderer(t *testing.T) {
	// A renderer whose options struct is missing from the sample list would
	// escape the Attrs check above, so the two are tied together.
	assert.NotEmpty(t, optionsSamples())
	seen := map[string]bool{}
	for _, sample := range optionsSamples() {
		name := reflect.TypeOf(sample).Name()
		assert.False(t, seen[name], "duplicate sample %s", name)
		seen[name] = true
		assert.True(t, strings.HasSuffix(name, "Opts"), "%s is not an options struct", name)
	}
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

// A declared TestID must reach the DOM. Every renderer accepts Attrs, so a
// renderer that ignores them looks configurable while silently dropping the
// caller's class, test id and ARIA overrides - which is how Tooltip shipped
// with an Attrs field nothing read. Reflection covers every renderer, so a new
// one cannot regress this quietly.
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
			require.True(t, attrs.IsValid(), "options must embed Attrs")
			attrs.FieldByName("TestID").SetString("probe-id")
			attrs.FieldByName("Class").SetString("probe-class")

			out := fn.Call([]reflect.Value{opts})
			component, ok := out[0].Interface().(templ.Component)
			require.True(t, ok, "renderer must return a templ.Component")

			html := renderComponent(t, component)
			assert.Contains(t, html, `data-testid="probe-id"`,
				"%s ignores Attrs.TestID, so no test can target it", name)
			assert.Contains(t, html, "probe-class",
				"%s ignores Attrs.Class, so callers cannot extend it", name)
		})
	}
}

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
	}
}

// renderers maps every exported renderer to its function value so reflection
// can exercise all of them. TestEveryRendererPropagatesItsAttrs checks this
// table against the AST scan, so it cannot fall behind the package.
func renderers() map[string]any {
	return map[string]any{
		"AlertDialog": AlertDialog, "Badge": Badge, "Banner": Banner, "Card": Card,
		"CardFooter": CardFooter, "CardHeader": CardHeader, "Checkbox": Checkbox, "Container": Container,
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
}

// A separator is not a command: it carries no label, no href and no handler, so
// rendering it as an item would put an empty, focusable row in the menu.
func TestMenuSeparatorRendersAsSeparator(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{
			{Label: "Rename", Href: "/x"},
			{Separator: true},
			{Label: "Delete", Href: "/y", Kind: KindDanger, Confirm: "Delete this?"},
		},
	}))

	assert.Contains(t, html, `role="separator"`)
	assert.Equal(t, 2, strings.Count(html, "<a "),
		"a separator must not render as a link")
	assert.Contains(t, html, `hx-confirm="Delete this?"`,
		"a destructive item declares its confirmation in the contract")
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
