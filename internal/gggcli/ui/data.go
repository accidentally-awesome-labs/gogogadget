package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"charm.land/huh/v2"
	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// Row types are plain data the screens render. Loading goes through the
// controller's Execute/Preview boundary — never a second engine. The committed
// intent file is read for display only.

type homeData struct {
	project    string
	modules    int
	slots      int
	deployment string
}

func (h homeData) view() string {
	return joinLines([]string{
		"Project   " + h.project,
		"Modules   " + strconv.Itoa(h.modules),
		"Slots     " + strconv.Itoa(h.slots),
		"Deploy    " + h.deployment,
		"",
		"Catalog · Providers · Plan · Conflicts · Tasks · Diagnostics",
	})
}

type catalogRow struct {
	id    string
	state string
	title string
}

type providerRow struct {
	slot        string
	development string
	test        string
	production  string
}

type conflictRow struct {
	module string
	path   string
	state  string
}

type taskRow struct {
	name    string
	outcome string
}

type diagnosticRow struct {
	severity string
	code     string
	message  string
}

// payloadEntries re-marshal controller payloads into the console's display
// rows: the envelope payload is JSON-shaped, so a round-trip keeps the ui
// package decoupled from gggcli's internal row types.
func payloadEntries(value any, out any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, out) == nil
}

func loadHome(cc gggcli.CommandContext) homeData {
	data := homeData{project: "not initialized"}
	if cc.Controller == nil {
		return data
	}
	raw, err := os.ReadFile(filepath.Join(cc.Controller.Root(), modkit.ProjectFileName))
	if err != nil {
		return data
	}
	project, err := modkit.ParseProject(raw)
	if err != nil {
		return data
	}
	data.project = "registry " + project.Registries[0].Namespace
	data.modules = len(project.Modules)
	data.slots = len(project.Providers)
	data.deployment = project.Deployment
	if data.deployment == "" {
		data.deployment = "none selected"
	}
	return data
}

func loadCatalog(cc gggcli.CommandContext) []catalogRow {
	result, err := cc.Controller.Execute(context.Background(), gggcli.CatalogRequest{})
	if err != nil {
		return nil
	}
	var entries []struct {
		ID    string `json:"id"`
		State string `json:"state"`
		Title string `json:"title"`
	}
	if !payloadEntries(result.Payload["modules"], &entries) {
		return nil
	}
	rows := make([]catalogRow, 0, len(entries))
	for _, m := range entries {
		rows = append(rows, catalogRow{id: m.ID, state: m.State, title: m.Title})
	}
	return rows
}

func loadConflicts(cc gggcli.CommandContext) []conflictRow {
	result, err := cc.Controller.Execute(context.Background(), gggcli.DiffRequest{Upstream: true})
	if err != nil {
		return nil
	}
	var files []struct {
		Module string `json:"module"`
		Path   string `json:"path"`
		State  string `json:"state"`
		Diff   string `json:"diff"`
	}
	if !payloadEntries(result.Payload["files"], &files) {
		return nil
	}
	rows := make([]conflictRow, 0, len(files))
	for _, f := range files {
		if f.Diff == "" {
			continue
		}
		rows = append(rows, conflictRow{module: f.Module, path: f.Path, state: f.State})
	}
	return rows
}

func loadProviders(cc gggcli.CommandContext) []providerRow {
	if cc.Controller == nil {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(cc.Controller.Root(), modkit.ProjectFileName))
	if err != nil {
		return nil
	}
	project, parseErr := modkit.ParseProject(raw)
	if parseErr != nil {
		return nil
	}
	slots := make([]string, 0, len(project.Providers))
	for slot := range project.Providers {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	rows := make([]providerRow, 0, len(slots))
	for _, slot := range slots {
		choice := project.Providers[slot]
		rows = append(rows, providerRow{
			slot:        slot,
			development: choice.Development.Adapter + "@" + choice.Development.Target,
			test:        choice.Test.Adapter + "@" + choice.Test.Target,
			production:  choice.Production.Adapter + "@" + choice.Production.Target,
		})
	}
	return rows
}

func loadDiagnostics(cc gggcli.CommandContext) []diagnosticRow {
	result, err := cc.Controller.Execute(context.Background(), gggcli.DoctorRequest{})
	if err != nil && len(result.Envelope.Diagnostics) == 0 {
		return nil
	}
	rows := make([]diagnosticRow, 0, len(result.Envelope.Diagnostics))
	for _, d := range result.Envelope.Diagnostics {
		rows = append(rows, diagnosticRow{severity: d.Severity, code: d.Code, message: d.Message})
	}
	return rows
}

func loadTasks(cc gggcli.CommandContext) []taskRow {
	rows := []taskRow{}
	if len(loadConflicts(cc)) == 0 {
		rows = append(rows, taskRow{name: "sync check", outcome: "clean"})
	} else {
		rows = append(rows, taskRow{name: "sync check", outcome: "drift; see Conflicts"})
	}
	if len(loadDiagnostics(cc)) == 0 {
		rows = append(rows, taskRow{name: "doctor", outcome: "ok"})
	} else {
		rows = append(rows, taskRow{name: "doctor", outcome: "findings; see Diagnostics"})
	}
	return rows
}

// previewSync builds the plan the Plan screen shows. Nothing writes: this is
// the same Preview the flags use.
func previewSync(cc gggcli.CommandContext) []string {
	plan, err := cc.Controller.Preview(context.Background(), gggcli.SyncMutation{})
	if err != nil {
		return []string{"plan failed: " + err.Error()}
	}
	if plan.Local == nil {
		return nil
	}
	lines := make([]string, 0, len(plan.Local.Changes))
	for _, change := range plan.Local.Changes {
		if change.Kind == modkit.ChangeUnchanged {
			continue
		}
		lines = append(lines, "  "+string(change.Kind)+"  "+string(change.Class)+"  "+change.Path)
	}
	return lines
}

// applySync applies the previewed sync through the same Apply the flags use.
func applySync(cc gggcli.CommandContext) (gggcli.Result, error) {
	plan, err := cc.Controller.Preview(context.Background(), gggcli.SyncMutation{})
	if err != nil {
		return gggcli.Result{}, err
	}
	return cc.Controller.Apply(context.Background(), plan)
}

// summarizeSync renders the applied sync outcome shown after the console
// exits.
func summarizeSync(result gggcli.Result) string {
	summary := "sync applied"
	for _, path := range result.Envelope.Generated {
		summary += "\n  wrote " + path
	}
	return summary + "\n"
}

// applyResolve resolves one staged conflict through the controller.
func applyResolve(cc gggcli.CommandContext, row conflictRow, mode modkit.ResolutionMode) (gggcli.Result, error) {
	plan, err := cc.Controller.Preview(context.Background(), gggcli.ResolveMutation{ModuleID: row.module, Path: row.path, Mode: mode})
	if err != nil {
		return gggcli.Result{}, err
	}
	return cc.Controller.Apply(context.Background(), plan)
}

// promptResolve renders the Huh select for a conflict resolution. Accessible
// mode comes from the command context (--accessible or GGG_ACCESSIBLE=1), and
// Huh renders the same options linearly there. An empty mode means dismissed.
func promptResolve(cc gggcli.CommandContext, row conflictRow) (modkit.ResolutionMode, error) {
	var choice string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Resolve " + row.module + " at " + row.path).
				Options(huh.NewOptions(
					string(modkit.ResolutionKeepLocal),
					string(modkit.ResolutionAcceptUpstream),
					string(modkit.ResolutionMerged),
				)...).
				Value(&choice),
		),
	).WithAccessible(cc.Accessible).WithShowHelp(false)
	if err := form.RunWithContext(context.Background()); err != nil {
		if err == huh.ErrUserAborted {
			return "", nil
		}
		return "", err
	}
	return modkit.ResolutionMode(choice), nil
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}
