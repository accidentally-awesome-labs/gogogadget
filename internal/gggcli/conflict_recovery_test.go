package gggcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The documented recovery from an update conflict is two commands: `ggg
// update` stages the upstream candidate and exits 4, then `ggg resolve`
// decides the file. It could not work. `reconcile` built the candidate and the
// diff into `Plan.Staged`, `Apply` wrote `plan.Changes` and the lock and
// nothing else, so the artifact the lock pointed at was never created and
// `ResolveConflict` refused every mode — including `--keep-local`, which
// discards upstream by definition — telling the operator to run the very
// command that had just failed to produce it.
//
// Nothing caught it because every conflict test wrote `Plan.Staged` itself.
// This gate is the answer to that: it drives `update` and then `resolve` in
// each mode through the CLI, over a conflict the CLI produced, and no helper
// here supplies a byte that a production path owes. Delete any assertion below
// and what is left still runs the real verbs; add a helper that stages the
// candidate and the gate is worthless again.
func TestCLIConflictRecoveryDrivesEveryResolveMode(t *testing.T) {
	merged := []byte("package button\n\nconst ButtonVersion = 2 // merged by hand\n")

	tests := []struct {
		name string
		flag string
		// premerge is the hand-merge `--merged` documents; the other two
		// modes leave the working tree exactly as the conflict found it.
		premerge  []byte
		wantBytes func(fixture stagedConflict) []byte
		wantState modkit.FileState
	}{
		{
			name: "accept-upstream takes the candidate bytes",
			flag: "--accept-upstream",
			wantBytes: func(fixture stagedConflict) []byte {
				return fixture.upstream
			},
			wantState: modkit.FileClean,
		},
		{
			name: "keep-local keeps the local bytes and advances the base",
			flag: "--keep-local",
			wantBytes: func(fixture stagedConflict) []byte {
				return fixture.local
			},
			wantState: modkit.FileModified,
		},
		{
			name:     "merged records the hand-merged bytes against the new base",
			flag:     "--merged",
			premerge: merged,
			wantBytes: func(stagedConflict) []byte {
				return merged
			},
			wantState: modkit.FileModified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := cliConflictProject(t)
			conflict := fixture.conflict

			// `update` left the candidate on disk, and the exit-4 message
			// names the command that consumes it rather than itself.
			if !strings.Contains(fixture.updateOut, conflict.CandidatePath) {
				t.Fatalf("update did not name the staged candidate:\n%s", fixture.updateOut)
			}
			advice := fixture.updateOut + fixture.updateErr + fixture.updateMessage
			wantAdvice := "`ggg resolve ggg/element/button --path " + fixture.target + "`"
			if !strings.Contains(advice, wantAdvice) {
				t.Fatalf("exit-4 message does not name %s:\n%s", wantAdvice, advice)
			}
			if !strings.Contains(advice, conflict.CandidatePath) {
				t.Fatalf("exit-4 message does not name the staged candidate:\n%s", advice)
			}
			if strings.Contains(advice, "ggg update") {
				t.Fatalf("exit-4 message sends the operator back through update:\n%s", advice)
			}

			// The documented middle step: read the diff before deciding.
			diffOut, diffErrOut, diffErr := runApp(t, fixture.root, fixture.engine, "diff", "--upstream")
			if diffErr != nil {
				t.Fatalf("diff --upstream = %v\n%s%s", diffErr, diffOut, diffErrOut)
			}
			if !strings.Contains(diffOut, conflict.DiffPath) {
				t.Fatalf("diff --upstream did not name the staged diff:\n%s", diffOut)
			}

			if tt.premerge != nil {
				writeTestFile(t, fixture.root, fixture.target, tt.premerge)
			}
			out, errOut, err := runApp(t, fixture.root, fixture.engine,
				"resolve", "ggg/element/button", "--path", fixture.target, tt.flag)
			if err != nil {
				t.Fatalf("resolve %s = %v\n%s%s", tt.flag, err, out, errOut)
			}

			want := tt.wantBytes(fixture)
			if got := readTestFile(t, fixture.root, fixture.target); string(got) != string(want) {
				t.Fatalf("resolved %s = %q, want %q", fixture.target, got, want)
			}

			lock, parseErr := modkit.ParseLock(readTestFile(t, fixture.root, modkit.LockFileName))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			var button *modkit.LockedModule
			for i := range lock.Modules {
				if lock.Modules[i].ID == "ggg/element/button" {
					button = &lock.Modules[i]
				}
			}
			if button == nil {
				t.Fatal("the resolved lock lost ggg/element/button")
			}
			if button.Pending != nil {
				t.Fatalf("the conflict is still pending after resolve: %#v", button.Pending)
			}
			if button.Revision != 2 {
				t.Fatalf("resolved revision = %d, want the upstream 2", button.Revision)
			}
			var file *modkit.LockedFile
			for i := range button.Files {
				if button.Files[i].Path == fixture.target {
					file = &button.Files[i]
				}
			}
			if file == nil {
				t.Fatalf("the resolved lock has no row for %s", fixture.target)
			}
			// Every mode advances the base to upstream — that is what clears
			// the conflict for good — and records what is actually on disk.
			if file.BaseSHA256 != conflict.CandidateSHA256 {
				t.Fatalf("base sha = %q, want the candidate's %q", file.BaseSHA256, conflict.CandidateSHA256)
			}
			if file.LocalSHA256 != sha256Hex(want) {
				t.Fatalf("local sha = %q, want %q", file.LocalSHA256, sha256Hex(want))
			}
			if file.State != tt.wantState {
				t.Fatalf("resolved state = %q, want %q", file.State, tt.wantState)
			}

			// The decided conflict's artifacts leave the way they arrived:
			// named in the plan, then gone.
			for _, path := range []string{conflict.CandidatePath, conflict.DiffPath} {
				if !strings.Contains(out, path) {
					t.Fatalf("resolve removed %s without naming it:\n%s", path, out)
				}
				if _, statErr := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(path))); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("resolved artifact %s survived: %v", path, statErr)
				}
			}

			// The tree settles: a following sync has nothing left to do about
			// this file, which is the operator's actual finish line.
			if _, _, syncErr := runApp(t, fixture.root, fixture.engine, "sync", "--check", "--offline"); syncErr != nil {
				t.Fatalf("sync --check after resolve = %v", syncErr)
			}
		})
	}

	// `--keep-local` discards upstream by definition, so it never needed the
	// staged bytes; the refusal that blocked it was a second defect on top of
	// the missing writer. Nothing else reads them either — resolution takes
	// upstream from the registry snapshot pinned to the conflict's commit —
	// so deleting the whole scratch root leaves every mode working.
	t.Run("no mode depends on the staged artifacts", func(t *testing.T) {
		for _, flag := range []string{"--keep-local", "--accept-upstream", "--merged"} {
			t.Run(flag, func(t *testing.T) {
				fixture := cliConflictProject(t)
				if err := os.RemoveAll(filepath.Join(fixture.root, "tmp")); err != nil {
					t.Fatal(err)
				}
				out, errOut, err := runApp(t, fixture.root, fixture.engine,
					"resolve", "ggg/element/button", "--path", fixture.target, flag)
				if err != nil {
					t.Fatalf("resolve %s without staged artifacts = %v\n%s%s", flag, err, out, errOut)
				}
				want := fixture.local
				if flag == "--accept-upstream" {
					want = fixture.upstream
				}
				if got := readTestFile(t, fixture.root, fixture.target); string(got) != string(want) {
					t.Fatalf("resolved %s = %q, want %q", fixture.target, got, want)
				}
			})
		}
	})
}

// stagedProbeGenerator renders like the real pipeline, records which staged
// artifacts are already on disk when generation runs, and then fails. That is
// the one point inside Apply that is after the staged write and before the
// lock, so it is where a rollback claim can actually be tested.
type stagedProbeGenerator struct {
	modkit.RegistryGenerator
	present *[]string
}

func (g stagedProbeGenerator) Generate(_ context.Context, plan modkit.Plan) error {
	for _, change := range plan.Changes {
		if change.Class != modkit.DestinationStaged {
			continue
		}
		if _, err := os.Stat(filepath.Join(plan.Root, filepath.FromSlash(change.Path))); err == nil {
			*g.present = append(*g.present, change.Path)
		}
	}
	return errors.New("generation refused by the rollback probe")
}

// The staged artifacts are journalled like every other planned write, so an
// apply that fails after writing them puts the tree back — including the
// directories it created under the scratch root. The probe proves the failure
// happens after the write rather than before it, which is the difference
// between a rollback test and a test that nothing was ever attempted.
func TestCLIConflictStagingRollsBackWithTheTransaction(t *testing.T) {
	fixture := cliConflictSetup(t)
	lockBefore := readTestFile(t, fixture.root, modkit.LockFileName)
	intentBefore := readTestFile(t, fixture.root, modkit.ProjectFileName)

	present := make([]string, 0, 2)
	engine := modkit.New(modkit.Options{
		Source:    fixture.source,
		Generator: stagedProbeGenerator{present: &present},
	})
	out, errOut, err := runApp(t, fixture.root, engine, "update", "--registry", "ggg", "--ref", "v2")
	if err == nil || exitOf(t, err) != 5 {
		t.Fatalf("update with a failing generator = %v, want exit 5\n%s%s", err, out, errOut)
	}
	if len(present) != 2 {
		t.Fatalf("generation saw %v staged artifacts on disk, want the candidate and its diff", present)
	}

	if _, statErr := os.Stat(filepath.Join(fixture.root, "tmp", "ggg")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the rolled-back apply left the staging root behind: %v", statErr)
	}
	if got := readTestFile(t, fixture.root, modkit.LockFileName); string(got) != string(lockBefore) {
		t.Fatal("the rolled-back apply moved the lock")
	}
	if got := readTestFile(t, fixture.root, modkit.ProjectFileName); string(got) != string(intentBefore) {
		t.Fatal("the rolled-back apply moved the intent")
	}
	if got := readTestFile(t, fixture.root, fixture.target); string(got) != string(fixture.local) {
		t.Fatalf("the rolled-back apply moved local bytes: %q", got)
	}
}
