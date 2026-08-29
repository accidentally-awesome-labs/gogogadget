package modkit

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func findFinding(t *testing.T, report HealthReport, code string) HealthFinding {
	t.Helper()
	for _, finding := range report.Findings {
		if finding.Code == code {
			return finding
		}
	}
	t.Fatalf("report %#v has no %q finding", report.Findings, code)
	return HealthFinding{}
}

func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || !entry.Type().IsRegular() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = digestBytes(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree: %v", err)
	}
	return snapshot
}

func TestHealthReportsConflictCandidates(t *testing.T) {
	t.Run("healthy pending state", func(t *testing.T) {
		fixture := prepareConflictFixture(t)
		materializeConflictPlan(t, fixture.root, fixture.plan)
		before := snapshotTree(t, fixture.root)
		report, err := fixture.engine.Health(context.Background(), fixture.root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if !report.Ok {
			t.Fatalf("healthy report = ok %t findings %#v", report.Ok, report.Findings)
		}
		wantFindings := []HealthFinding{
			{Code: "module_pinned", Severity: "warn", Module: "component/card"},
			{Code: "conflict_pending", Severity: "warn", Module: "element/button", Path: "internal/modules/button.go"},
		}
		if len(report.Findings) != len(wantFindings) {
			t.Fatalf("healthy findings = %#v, want %#v", report.Findings, wantFindings)
		}
		for i, want := range wantFindings {
			got := report.Findings[i]
			if got.Code != want.Code || got.Severity != want.Severity || got.Module != want.Module || got.Path != want.Path {
				t.Fatalf("finding[%d] = %#v, want %#v", i, got, want)
			}
		}
		if got, want := report.RegistryCommit, testCommitB; got != want {
			t.Fatalf("registry commit = %q, want %q", got, want)
		}
		if got, want := len(report.Conflicts), 1; got != want {
			t.Fatalf("reported conflicts = %#v", report.Conflicts)
		}
		if after := snapshotTree(t, fixture.root); !reflect.DeepEqual(before, after) {
			t.Fatal("Health mutated the project tree")
		}
		sourceFree, err := New(Options{}).Health(context.Background(), fixture.root)
		if err != nil {
			t.Fatalf("Health(source-free): %v", err)
		}
		if !reflect.DeepEqual(report.Conflicts, sourceFree.Conflicts) || sourceFree.Ok != report.Ok {
			t.Fatalf("source-free report diverged: %#v", sourceFree)
		}
	})

	t.Run("missing candidate", func(t *testing.T) {
		fixture := prepareConflictFixture(t)
		materializeConflictPlan(t, fixture.root, fixture.plan)
		conflict := fixture.plan.Conflicts[0]
		if err := os.Remove(filepath.Join(fixture.root, filepath.FromSlash(conflict.CandidatePath))); err != nil {
			t.Fatalf("remove candidate: %v", err)
		}
		report, err := fixture.engine.Health(context.Background(), fixture.root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if report.Ok {
			t.Fatalf("missing-candidate report = ok %t findings %#v", report.Ok, report.Findings)
		}
		finding := findFinding(t, report, "candidate_missing")
		if finding.Module != "element/button" || finding.Path != conflict.CandidatePath {
			t.Fatalf("finding = %#v", finding)
		}
		if !strings.Contains(finding.Message, testCommitB) {
			t.Fatalf("finding message omits pending commit: %q", finding.Message)
		}
	})

	t.Run("tampered candidate", func(t *testing.T) {
		fixture := prepareConflictFixture(t)
		materializeConflictPlan(t, fixture.root, fixture.plan)
		conflict := fixture.plan.Conflicts[0]
		writeTestFile(t, fixture.root, conflict.CandidatePath, []byte("tampered"))
		report, err := fixture.engine.Health(context.Background(), fixture.root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if report.Ok {
			t.Fatalf("tampered-candidate report = ok %t findings %#v", report.Ok, report.Findings)
		}
		if finding := findFinding(t, report, "candidate_mismatch"); finding.Module != "element/button" {
			t.Fatalf("finding = %#v", finding)
		}
	})

	t.Run("unreadable candidate artifacts", func(t *testing.T) {
		t.Run("directory at candidate path", func(t *testing.T) {
			fixture := prepareConflictFixture(t)
			materializeConflictPlan(t, fixture.root, fixture.plan)
			conflict := fixture.plan.Conflicts[0]
			candidatePath := filepath.Join(fixture.root, filepath.FromSlash(conflict.CandidatePath))
			if err := os.Remove(candidatePath); err != nil {
				t.Fatalf("remove candidate: %v", err)
			}
			if err := os.MkdirAll(candidatePath, 0o755); err != nil {
				t.Fatalf("mkdir candidate: %v", err)
			}
			report, err := fixture.engine.Health(context.Background(), fixture.root)
			if err != nil {
				t.Fatalf("Health: %v", err)
			}
			if report.Ok {
				t.Fatalf("directory-candidate report = %#v", report.Findings)
			}
			findFinding(t, report, "candidate_unreadable")
		})

		t.Run("symlinked candidate", func(t *testing.T) {
			fixture := prepareConflictFixture(t)
			materializeConflictPlan(t, fixture.root, fixture.plan)
			conflict := fixture.plan.Conflicts[0]
			candidatePath := filepath.Join(fixture.root, filepath.FromSlash(conflict.CandidatePath))
			outside := filepath.Join(t.TempDir(), "decoy")
			writeTestFile(t, filepath.Dir(outside), filepath.Base(outside), []byte("decoy"))
			if err := os.Remove(candidatePath); err != nil {
				t.Fatalf("remove candidate: %v", err)
			}
			if err := os.Symlink(outside, candidatePath); err != nil {
				t.Fatalf("symlink candidate: %v", err)
			}
			report, err := fixture.engine.Health(context.Background(), fixture.root)
			if err != nil {
				t.Fatalf("Health: %v", err)
			}
			if report.Ok {
				t.Fatalf("symlink-candidate report = %#v", report.Findings)
			}
			findFinding(t, report, "candidate_unreadable")
		})
	})

	t.Run("missing candidates report in path order", func(t *testing.T) {
		firstRegistry, secondRegistry := conflictRegistries(t)
		source := refSource{snapshots: map[string]Snapshot{
			"v1": {Commit: testCommitA, FS: firstRegistry},
			"v2": {Commit: testCommitB, FS: secondRegistry},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:   1,
			Registry: ProjectRegistry{Repository: "local/registry", Ref: "v1"},
			Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		writeTestFile(t, root, "internal/modules/button.go", []byte("package button\n\nconst LocalA = true\n"))
		writeTestFile(t, root, "internal/modules/button_helper.go", []byte("package button\n\nconst LocalB = true\n"))
		update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
		if err != nil {
			t.Fatalf("Plan(update): %v", err)
		}
		materializeConflictPlan(t, root, update)
		for _, staged := range update.Staged {
			if strings.HasSuffix(staged.Path, ".candidate") {
				if err := os.Remove(filepath.Join(root, filepath.FromSlash(staged.Path))); err != nil {
					t.Fatalf("remove candidate %s: %v", staged.Path, err)
				}
			}
		}
		report, err := engine.Health(context.Background(), root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		var pendingPaths, missingCandidates []string
		for _, finding := range report.Findings {
			switch finding.Code {
			case "conflict_pending":
				pendingPaths = append(pendingPaths, finding.Path)
			case "candidate_missing":
				missingCandidates = append(missingCandidates, finding.Path)
			}
		}
		wantPending := []string{"internal/modules/button.go", "internal/modules/button_helper.go"}
		if !slices.Equal(pendingPaths, wantPending) {
			t.Fatalf("conflict_pending findings not in conflict-path order: %v, want %v", pendingPaths, wantPending)
		}
		if len(missingCandidates) != 2 {
			t.Fatalf("candidate_missing findings = %v, want two", missingCandidates)
		}
		if got, want := len(report.Conflicts), 2; got != want {
			t.Fatalf("reported conflicts = %d, want %d", got, want)
		}
	})

	t.Run("stale conflicted target", func(t *testing.T) {
		fixture := prepareConflictFixture(t)
		materializeConflictPlan(t, fixture.root, fixture.plan)
		writeTestFile(t, fixture.root, "internal/modules/button.go", []byte("package button\n\nconst Changed = true\n"))
		report, err := fixture.engine.Health(context.Background(), fixture.root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if !report.Ok {
			t.Fatalf("stale target should stay a warning: %#v", report.Findings)
		}
		if finding := findFinding(t, report, "conflict_stale"); finding.Module != "element/button" {
			t.Fatalf("finding = %#v", finding)
		}
	})

	t.Run("missing lock", func(t *testing.T) {
		firstRegistry, _ := conflictRegistries(t)
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:   1,
			Registry: ProjectRegistry{Repository: "local/registry", Ref: "v1"},
			Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
		})
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: firstRegistry}}})
		report, err := engine.Health(context.Background(), root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if report.Ok || len(report.Findings) != 1 || report.Findings[0].Code != "lock_missing" {
			t.Fatalf("missing-lock report = %#v", report)
		}
	})

	t.Run("invalid lock", func(t *testing.T) {
		firstRegistry, _ := conflictRegistries(t)
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:   1,
			Registry: ProjectRegistry{Repository: "local/registry", Ref: "v1"},
			Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
		})
		writeTestFile(t, root, "gogogadget.lock.json", []byte(`{"schema":1,`))
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: firstRegistry}}})
		report, err := engine.Health(context.Background(), root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if report.Ok || len(report.Findings) != 1 || report.Findings[0].Code != "lock_invalid" {
			t.Fatalf("invalid-lock report = %#v", report)
		}
	})

	t.Run("missing project", func(t *testing.T) {
		firstRegistry, _ := conflictRegistries(t)
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: firstRegistry}}})
		report, err := engine.Health(context.Background(), t.TempDir())
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if report.Ok || len(report.Findings) != 1 || report.Findings[0].Code != "project_missing" {
			t.Fatalf("missing-project report = %#v", report)
		}
	})

	t.Run("clean lock without pending state", func(t *testing.T) {
		firstRegistry, _ := conflictRegistries(t)
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:   1,
			Registry: ProjectRegistry{Repository: "local/registry", Ref: "v1"},
			Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
		})
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: firstRegistry}}})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		report, err := engine.Health(context.Background(), root)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if !report.Ok || len(report.Findings) != 0 {
			t.Fatalf("clean report = ok %t findings %#v", report.Ok, report.Findings)
		}
	})
}

func TestUpdateRematerializesMissingCandidates(t *testing.T) {
	fixture := prepareConflictFixture(t)
	materializeConflictPlan(t, fixture.root, fixture.plan)
	conflict := fixture.plan.Conflicts[0]
	candidatePath := filepath.Join(fixture.root, filepath.FromSlash(conflict.CandidatePath))
	localPath := filepath.Join(fixture.root, "internal/modules/button.go")
	localBefore, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local button: %v", err)
	}
	if err := os.Remove(candidatePath); err != nil {
		t.Fatalf("remove candidate: %v", err)
	}

	pendingCommit := ""
	for _, module := range fixture.plan.Lock.Modules {
		if module.ID == "element/button" && module.Pending != nil {
			pendingCommit = module.Pending.RegistryCommit
		}
	}
	if pendingCommit == "" {
		t.Fatal("fixture has no pending element/button module")
	}
	rematerialized, err := fixture.engine.Plan(context.Background(), fixture.root, Operation{Kind: OpUpdate, RegistryRef: pendingCommit})
	if err != nil {
		t.Fatalf("Plan(rematerialize): %v", err)
	}
	if got := rematerialized.RegistryCommit; got != pendingCommit {
		t.Fatalf("rematerialized at commit %q, want pending commit %q", got, pendingCommit)
	}
	var staged, stagedDiff StagedFile
	for _, candidate := range rematerialized.Staged {
		switch candidate.Path {
		case conflict.CandidatePath:
			staged = candidate
		case conflict.DiffPath:
			stagedDiff = candidate
		}
	}
	if staged.Path == "" || stagedDiff.Path == "" {
		t.Fatalf("update did not re-stage artifacts %s/%s: %#v", conflict.CandidatePath, conflict.DiffPath, rematerialized.Staged)
	}
	if staged.SHA256 != conflict.UpstreamSHA256 || digestBytes(staged.Content) != conflict.UpstreamSHA256 {
		t.Fatalf("re-staged candidate digest = %q, want %q", staged.SHA256, conflict.UpstreamSHA256)
	}
	for _, change := range rematerialized.Changes {
		if change.Path == "internal/modules/button.go" {
			t.Fatalf("rematerialization touched local source: %#v", change)
		}
	}
	localAfter, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local button after: %v", err)
	}
	if !slices.Equal(localBefore, localAfter) {
		t.Fatal("rematerialization changed local button bytes")
	}
	if _, err := os.Stat(candidatePath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Plan wrote candidate bytes during planning: %v", err)
	}

	materializeConflictPlan(t, fixture.root, rematerialized)
	report, err := fixture.engine.Health(context.Background(), fixture.root)
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !report.Ok {
		t.Fatalf("health after rematerialization = %#v", report)
	}
}
