package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// screen enumerates the console's screens.
type screen int

const (
	screenHome screen = iota
	screenCatalog
	screenProviders
	screenPlan
	screenConflicts
	screenTasks
	screenDiagnostics
	screenHelp
)

var screenTitles = map[screen]string{
	screenHome:        "Home",
	screenCatalog:     "Catalog",
	screenProviders:   "Providers",
	screenPlan:        "Plan",
	screenConflicts:   "Conflicts",
	screenTasks:       "Tasks",
	screenDiagnostics: "Diagnostics",
	screenHelp:        "Help",
}

// decision records how the console ended.
type decision int

const (
	decidedNothing decision = iota
	cancelledBeforePlan
	cancelledAfterPlan
	applied
)

// model is the Bubble Tea root model. Screens share one cursor, one search
// string, and one back stack.
type model struct {
	cc       gggcli.CommandContext
	width    int
	height   int
	current  screen
	stack    []screen
	cursor   int
	search   string
	decision decision
	status   string
	err      error

	home        homeData
	catalog     []catalogRow
	providers   []providerRow
	plan        []string
	planStaged  bool
	conflicts   []conflictRow
	tasks       []taskRow
	diagnostics []diagnosticRow
}

func newModel(cc gggcli.CommandContext) model {
	m := model{cc: cc, width: 80, height: 24, current: screenHome}
	m.home = loadHome(cc)
	return m
}

// Init issues the initial window size so View has real dimensions.
func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// handleKey implements the console key contract: Esc back, ? help, / search,
// Enter select/apply, Ctrl+C cancel.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.current == screenPlan && m.planStaged {
			m.decision = cancelledAfterPlan
		} else {
			m.decision = cancelledBeforePlan
		}
		return m, tea.Quit
	case "esc":
		if len(m.stack) == 0 {
			m.decision = cancelledBeforePlan
			return m, tea.Quit
		}
		m.current = m.stack[len(m.stack)-1]
		m.stack = m.stack[:len(m.stack)-1]
		m.cursor, m.search = 0, ""
		return m, nil
	case "?":
		m.stack = append(m.stack, m.current)
		m.current = screenHelp
		return m, nil
	case "/":
		m.search = ""
		return m, nil
	case "enter":
		return m.selectItem()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < m.rowCount()-1 {
			m.cursor++
		}
		return m, nil
	default:
		if m.current == screenCatalog && len(msg.String()) == 1 {
			// Typing after "/" filters the catalog.
			m.search += msg.String()
			m.cursor = 0
		}
		return m, nil
	}
}

// selectItem acts on the cursor row. On Plan, Enter applies the previewed
// sync through the same Apply the flags use.
func (m model) selectItem() (tea.Model, tea.Cmd) {
	switch m.current {
	case screenHome:
		switch m.cursor {
		case 0:
			m.push(screenCatalog)
		case 1:
			m.push(screenProviders)
		case 2:
			m.push(screenPlan)
		case 3:
			m.push(screenConflicts)
		case 4:
			m.push(screenTasks)
		case 5:
			m.push(screenDiagnostics)
		}
	case screenPlan:
		result, err := applySync(m.cc)
		if err != nil {
			if gggcli.ExitCode(err) == modkit.ExitRefusal {
				m.decision = cancelledAfterPlan
				return m, tea.Quit
			}
			m.err = err
			return m, tea.Quit
		}
		m.decision = applied
		m.status = summarizeSync(result)
		return m, tea.Quit
	case screenConflicts:
		if m.cursor < len(m.conflicts) {
			row := m.conflicts[m.cursor]
			mode, err := promptResolve(m.cc, row)
			if err != nil {
				m.err = err
				return m, tea.Quit
			}
			if mode == "" {
				// Dismissed the form before any plan existed.
				m.decision = cancelledBeforePlan
				return m, tea.Quit
			}
			_, applyErr := applyResolve(m.cc, row, mode)
			if applyErr != nil {
				m.err = applyErr
				return m, tea.Quit
			}
			m.conflicts = loadConflicts(m.cc)
			m.status = "resolved " + row.module + " " + row.path + "\n"
		}
	case screenCatalog:
		// A module row has no in-console mutation; Enter on a filtered list
		// resets the filter.
		m.search = ""
	}
	return m, nil
}

func (m *model) push(target screen) {
	m.stack = append(m.stack, m.current)
	m.current = target
	m.cursor, m.search = 0, ""
	if m.cc.Controller == nil {
		return
	}
	if target == screenConflicts {
		m.conflicts = loadConflicts(m.cc)
	}
	if target == screenProviders {
		m.providers = loadProviders(m.cc)
	}
	if target == screenPlan {
		m.plan = previewSync(m.cc)
		m.planStaged = true
	}
	if target == screenDiagnostics {
		m.diagnostics = loadDiagnostics(m.cc)
	}
	if target == screenTasks {
		m.tasks = loadTasks(m.cc)
	}
}

// rowCount is the number of selectable rows on the current screen.
func (m model) rowCount() int {
	switch m.current {
	case screenHome:
		return 6
	case screenCatalog:
		return len(m.filteredCatalog())
	case screenProviders:
		return len(m.providers)
	case screenConflicts:
		return len(m.conflicts)
	case screenTasks:
		return len(m.tasks)
	case screenDiagnostics:
		return len(m.diagnostics)
	default:
		return 0
	}
}

func (m model) filteredCatalog() []catalogRow {
	rows := make([]catalogRow, 0, len(m.catalog))
	for _, row := range m.catalog {
		if m.search == "" || strings.Contains(row.id, m.search) || strings.Contains(strings.ToLower(row.title), strings.ToLower(m.search)) {
			rows = append(rows, row)
		}
	}
	return rows
}

// View renders the current screen inside its width. Every screen opens with
// its title bar and closes with the key legend, so the console is legible at
// any size from 80x24 up.
func (m model) View() tea.View {
	var b strings.Builder
	title := screenTitles[m.current]
	if m.search != "" {
		title += "  search: " + m.search
	}
	b.WriteString(" " + title + "\n")
	b.WriteString(strings.Repeat("-", max(3, min(m.width-2, 78))) + "\n")

	body := m.body()
	for _, line := range strings.Split(body, "\n") {
		b.WriteString(" " + line + "\n")
	}
	b.WriteString(strings.Repeat("-", max(3, min(m.width-2, 78))) + "\n")
	b.WriteString(" Esc back · ? help · / search · Enter select/apply · Ctrl+C cancel\n")
	return tea.NewView(b.String())
}

func (m model) body() string {
	switch m.current {
	case screenHome:
		return m.home.view()
	case screenCatalog:
		return viewRows(m.filteredCatalog(), m.cursor, func(r catalogRow) string {
			return pad(r.id, 34) + pad(r.state, 12) + r.title
		})
	case screenProviders:
		return viewRows(m.providers, m.cursor, func(r providerRow) string {
			return pad(r.slot, 22) + pad(r.development, 26) + pad(r.test, 26) + r.production
		})
	case screenPlan:
		if len(m.plan) == 0 {
			return "The project is in sync; nothing to apply."
		}
		var lines []string
		lines = append(lines, "Sync would make these changes (nothing is written until you apply):")
		return strings.Join(append(lines, m.plan...), "\n")
	case screenConflicts:
		if len(m.conflicts) == 0 {
			return "No staged conflicts."
		}
		return viewRows(m.conflicts, m.cursor, func(r conflictRow) string {
			return pad(r.module, 30) + pad(r.path, 40) + r.state
		})
	case screenTasks:
		if len(m.tasks) == 0 {
			return "No task results yet."
		}
		return viewRows(m.tasks, m.cursor, func(r taskRow) string {
			return pad(r.name, 22) + r.outcome
		})
	case screenDiagnostics:
		if len(m.diagnostics) == 0 {
			return "No findings."
		}
		return viewRows(m.diagnostics, m.cursor, func(r diagnosticRow) string {
			return pad(r.severity, 8) + pad(r.code, 26) + r.message
		})
	case screenHelp:
		return "Keys:\n  Esc     back\n  ?       help\n  /       search (Catalog)\n  Enter   select / apply\n  Ctrl+C  cancel\n\nCancelling before a plan exists exits cleanly; cancelling after a preview\nis reported as user_cancelled and never writes."
	}
	return ""
}

// viewRows renders rows with a cursor marker, unicode-safe via lipgloss
// width.
func viewRows[T any](rows []T, cursor int, render func(T) string) string {
	if len(rows) == 0 {
		return "Nothing to show."
	}
	var b strings.Builder
	for i, row := range rows {
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		b.WriteString(marker + render(row) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func pad(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s + " "
	}
	return s + strings.Repeat(" ", width-w)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
