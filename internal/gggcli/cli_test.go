package gggcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// Usage failures are exit 2 and must never be confused with a runtime failure.
// The non-TTY no-arg invocation is the declared interactive_terminal_required
// usage failure.
func TestCLIRejectsUnknownAndMalformedCommands(t *testing.T) {
	cases := [][]string{
		nil,
		{"frobnicate"},
		// --help on an unknown command stays a usage failure: a machine
		// consumer must never read help as a successful run of nothing.
		{"frobnicate", "--help"},
		{"version", "extra"},
		{"info"},
		{"info", "element/button", "element/card"},
		{"add"},
		{"remove"},
		{"resolve", "element/button", "--path", "x.go"},
		{"resolve", "element/button", "--path", "x.go", "--keep-local", "--accept-upstream"},
		{"registry"},
		{"registry", "demolish"},
		{"sync", "--nope"},
		{"info", "not-an-id"},
		{"version", "--json"},
		{"cache", "prune", "--json"},
	}
	for _, args := range cases {
		_, _, err := runApp(t, t.TempDir(), nil, args...)
		if err == nil {
			t.Fatalf("Run(%v) = nil error, want usage failure", args)
		}
		if got := exitOf(t, err); got != 2 {
			t.Fatalf("Run(%v) exit = %d, want 2", args, got)
		}
	}
}

// version is the one command that works without a project.
func TestCLIVersionNeedsNoProject(t *testing.T) {
	out, _, err := runApp(t, t.TempDir(), nil, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if out != "ggg v1.2.3\n" {
		t.Fatalf("version output = %q", out)
	}
}

// Every --json command emits exactly the declared envelope. A missing key is a
// broken contract for every machine consumer.
func TestCLIJSONEnvelopeMatchesDeclaredSchema(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "init", "--adopt", "--json")
	if err != nil {
		t.Fatalf("init --adopt --json: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, out)
	}
	want := []string{
		"ok", "command", "run_id", "registry_commit",
		"resolved", "changes", "generated", "conflicts", "diagnostics", "exit",
	}
	for _, key := range want {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("envelope missing %q: %s", key, out)
		}
	}
	if len(envelope) != len(want) {
		t.Fatalf("envelope has %d keys, want exactly %d: %s", len(envelope), len(want), out)
	}
	if envelope["ok"] != true || envelope["command"] != "init" || envelope["exit"] != float64(0) {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope["run_id"] == "" {
		t.Fatal("run_id is empty")
	}
}

// init --adopt must produce a real project and lock on disk.
func TestCLIInitAdoptWritesProjectAndLock(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init --adopt: %v", err)
	}
	for _, name := range []string{"gogogadget.json", "gogogadget.lock.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("init did not write %s: %v", name, err)
		}
	}
}

// sync --check is the drift gate: clean after a sync, exit 4 the moment a
// generated output is edited or missing.
func TestCLISyncCheckDetectsTamperedAndMissingGeneratedOutput(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--check", "--offline"); err != nil {
		t.Fatalf("check on a clean tree: %v", err)
	}

	generated := filepath.Join(root, "internal", "modules", "bootstrap_registry_gen.go")
	original, err := os.ReadFile(generated)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	t.Run("tampered bytes", func(t *testing.T) {
		if err := os.WriteFile(generated, append(original, []byte("\n// drift\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(generated, original, 0o644) })
		_, _, err := runApp(t, root, engine, "sync", "--check", "--offline")
		if err == nil || exitOf(t, err) != 4 {
			t.Fatalf("tampered check = %v, want exit 4", err)
		}
	})
	t.Run("missing output", func(t *testing.T) {
		if err := os.Remove(generated); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(generated, original, 0o644) })
		_, _, err := runApp(t, root, engine, "sync", "--check", "--offline")
		if err == nil || exitOf(t, err) != 4 {
			t.Fatalf("missing check = %v, want exit 4", err)
		}
	})
}

// No byte leaves the tree without being named, and a file the tool did not
// write is never removed.
//
// The generation stage deletes every registry-owned output the selected graph
// no longer renders, and it used to do so from inside Apply, outside the plan:
// `Result.Deleted` reached no renderer, `renderHuman` prints `env.Changes`,
// and so `sync`, `add`, `update` and `remove` each deleted a planted file at a
// registry-owned name at exit 0 without naming it — in human output and in
// `--json` alike. `sync --check` counted it as a `generated_stale` diagnostic
// whose path the human renderer dropped, and the same exit 4 covered "would
// write a file" and "would delete bytes ggg did not write".
//
// Both halves are pinned here because the pair is the property: a marked
// aggregate is still swept AND named, and unmarked bytes refuse.
//
// Mutation: stop classifying unrendered outputs in Engine.Plan, and the marked
// case loses its change while the authored case is deleted at exit 0.
func TestCLISyncNamesStaleDeletionsAndRefusesAuthoredBytesAtAGeneratedName(t *testing.T) {
	// Any registry-owned name works; this one is a `_registry_gen.` infix in
	// a directory the fixture already installs into.
	const target = "internal/modules/aa_probe_registry_gen.go"
	if !modkit.IsRegistryOwnedOutputPath(target) {
		t.Fatalf("%s is not a registry-owned output; pick a name the pipeline generates", target)
	}
	marked := []byte("// Code generated " + modkit.GeneratedOutputMarker + ".\n//\n// index: 0\n\npackage modules\n")
	authored := []byte("package modules\n\n// A human wrote this and happened to like the name.\nconst AAProbe = \"authored\"\n")

	synced := func(t *testing.T, body []byte) (string, *modkit.Engine) {
		t.Helper()
		root, engine := cliProject(t)
		if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
			t.Fatalf("sync: %v", err)
		}
		writeTestFile(t, root, target, body)
		return root, engine
	}

	t.Run("a marked aggregate is a named delete and is swept", func(t *testing.T) {
		root, engine := synced(t, marked)

		out, _, err := runApp(t, root, engine, "sync", "--check", "--offline", "--json")
		if err == nil || exitOf(t, err) != 4 {
			t.Fatalf("check over a pending deletion = %v, want exit 4", err)
		}
		var envelope struct {
			Changes   []modkit.Change `json:"changes"`
			Generated []string        `json:"generated"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("envelope is not JSON: %v\n%s", err, out)
		}
		var deletion *modkit.Change
		for i := range envelope.Changes {
			if envelope.Changes[i].Path == target {
				deletion = &envelope.Changes[i]
			}
		}
		if deletion == nil {
			t.Fatalf("--json carries no change for the pending deletion: %s", out)
		}
		if deletion.Kind != modkit.ChangeDelete || deletion.Class != modkit.DestinationGenerated {
			t.Fatalf("deletion change = %#v, want delete/generated", *deletion)
		}
		if deletion.SHA256 != sha256Hex(marked) {
			t.Fatalf("deletion digest = %q, want the bytes being removed %q", deletion.SHA256, sha256Hex(marked))
		}
		// `generated` reports what the run WRITES. A delete listed there is
		// indistinguishable from a rendered aggregate.
		if slices.Contains(envelope.Generated, target) {
			t.Fatalf("a deleted path is listed under generated: %v", envelope.Generated)
		}

		// The human gate names it too.
		human, _, err := runApp(t, root, engine, "sync", "--check", "--offline")
		if err == nil || exitOf(t, err) != 4 {
			t.Fatalf("human check = %v, want exit 4", err)
		}
		if !strings.Contains(human, target) {
			t.Fatalf("the terminal gate names no path:\n%s", human)
		}

		applied, _, err := runApp(t, root, engine, "sync", "--offline")
		if err != nil {
			t.Fatalf("sync over a stale aggregate: %v", err)
		}
		if !strings.Contains(applied, target) {
			t.Fatalf("the deletion was not announced:\n%s", applied)
		}
		if _, err := os.Stat(filepath.Join(root, target)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the stale aggregate survived the sweep: %v", err)
		}
	})

	t.Run("authored bytes refuse, are named, and stay on disk", func(t *testing.T) {
		root, engine := synced(t, authored)
		lockBefore, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
		if err != nil {
			t.Fatal(err)
		}

		// Exit 3, not 4: "would delete bytes ggg did not write" is a refusal,
		// and the gate's exit code now says which of the two it found.
		human, humanErr, err := runApp(t, root, engine, "sync", "--check", "--offline")
		if err == nil || exitOf(t, err) != 3 {
			t.Fatalf("check over authored bytes = %v, want exit 3", err)
		}
		if !strings.Contains(human+humanErr, target) {
			t.Fatalf("the refusal names no path:\n%s%s", human, humanErr)
		}

		out, _, err := runApp(t, root, engine, "sync", "--offline", "--json")
		if err == nil || exitOf(t, err) != 3 {
			t.Fatalf("sync over authored bytes = %v, want exit 3", err)
		}
		var refusal struct {
			Diagnostics []modkit.Diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal([]byte(out), &refusal); err != nil {
			t.Fatalf("envelope is not JSON: %v\n%s", err, out)
		}
		coded := false
		for _, diagnostic := range refusal.Diagnostics {
			if diagnostic.Code == "generated_unowned" && diagnostic.Path == target {
				coded = true
			}
		}
		if !coded {
			t.Fatalf("--json carries no generated_unowned diagnostic naming the file: %s", out)
		}

		if _, err := os.Stat(filepath.Join(root, target)); err != nil {
			t.Fatalf("authored bytes were deleted by a refused command: %v", err)
		}
		lockAfter, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(lockBefore, lockAfter) {
			t.Fatal("the refused sync wrote the lock")
		}

		if _, _, err := runApp(t, root, engine, "add", "ggg/component/card"); err == nil || exitOf(t, err) != 3 {
			t.Fatalf("add over authored bytes = %v, want exit 3", err)
		}
	})

	// `remove` plans on its own path, and classified only inside Apply: it
	// reported exit 5 over a rolled-back tree where `sync` reported exit 3
	// over an untouched one — the same question answered two ways.
	t.Run("remove refuses on the plan, not inside the transaction", func(t *testing.T) {
		root, engine := cliProject(t)
		if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, _, err := runApp(t, root, engine, "add", "ggg/component/card"); err != nil {
			t.Fatalf("add: %v", err)
		}
		writeTestFile(t, root, target, authored)

		out, errOut, err := runApp(t, root, engine, "remove", "ggg/component/card")
		if err == nil || exitOf(t, err) != 3 {
			t.Fatalf("remove over authored bytes = %v, want exit 3\n%s%s", err, out, errOut)
		}
		if !strings.Contains(out+errOut, target) {
			t.Fatalf("remove refused without naming the path:\n%s%s", out, errOut)
		}
		if _, statErr := os.Stat(filepath.Join(root, target)); statErr != nil {
			t.Fatalf("remove deleted authored bytes: %v", statErr)
		}
	})

	// `resolve` is the tenth command form and the only one absent from the
	// evidence, which is backwards: it is the verb that runs over a tree the
	// operator has been hand-editing, so it is the likeliest of them all to
	// meet an unmarked file. It also builds its plan on its own path — the
	// same shape that made `remove` answer exit 5 where `sync` answered exit
	// 3 — so the shared verdict has to be measured here, not inferred.
	//
	// What these do NOT show is that `ggg resolve` works: nothing in
	// production writes `Plan.Staged`, so cliConflictProject has to
	// materialize the candidate itself. See the comment there before reading
	// a green run as coverage of the resolver.
	t.Run("resolve reaches the same verdict as every other plan producer", func(t *testing.T) {
		t.Run("a marked aggregate is a named delete", func(t *testing.T) {
			root, engine, conflicted := cliConflictProject(t)
			writeTestFile(t, root, target, marked)

			out, errOut, err := runApp(t, root, engine,
				"resolve", "ggg/element/button", "--path", conflicted, "--accept-upstream")
			if err != nil {
				t.Fatalf("resolve over a stale aggregate = %v\n%s%s", err, out, errOut)
			}
			if !strings.Contains(out, target) {
				t.Fatalf("resolve swept the aggregate without naming it:\n%s", out)
			}
			if _, statErr := os.Stat(filepath.Join(root, target)); !errors.Is(statErr, fs.ErrNotExist) {
				t.Fatalf("the stale aggregate survived resolve: %v", statErr)
			}
		})

		t.Run("authored bytes refuse, are named, and stay on disk", func(t *testing.T) {
			root, engine, conflicted := cliConflictProject(t)
			writeTestFile(t, root, target, authored)
			lockBefore, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
			if err != nil {
				t.Fatal(err)
			}

			out, errOut, err := runApp(t, root, engine,
				"resolve", "ggg/element/button", "--path", conflicted, "--accept-upstream")
			if err == nil || exitOf(t, err) != 3 {
				t.Fatalf("resolve over authored bytes = %v, want exit 3\n%s%s", err, out, errOut)
			}
			if !strings.Contains(out+errOut, target) {
				t.Fatalf("resolve refused without naming the path:\n%s%s", out, errOut)
			}
			if _, statErr := os.Stat(filepath.Join(root, target)); statErr != nil {
				t.Fatalf("resolve deleted authored bytes: %v", statErr)
			}
			lockAfter, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(lockBefore, lockAfter) {
				t.Fatal("the refused resolve wrote the lock")
			}
		})
	})
}

// A refusal is only as good as the move it names, and this one named a move
// that cannot work. The message said "Declare the file in a module's `files`
// if it is a payload, or delete it" — and declaring is refused for EVERY path
// the refusal can print: the Unowned set is filtered by
// IsRegistryOwnedOutputPath, IsGeneratedOutputPath is that predicate ORed with
// the external-tool outputs, and reconcilePlannedState refuses any payload
// whose target satisfies it. So half the guidance was a dead end, `--claim`
// does not reach it either, and the two moves that DO work — rename, restore
// the banner — went unmentioned.
//
// Nothing caught it because nothing followed the advice. This test does: each
// remedy the message names is executed and asserted to clear the refusal, and
// the one it forbids is asserted to be refused. Mutation: put "declare the
// file in a module's `files`" back as a remedy and the last subtest is the
// standing proof it is false.
func TestCLIRefusalNamesOnlyRemediesThatWork(t *testing.T) {
	const target = "internal/modules/aa_probe_registry_gen.go"
	authored := []byte("package modules\n\n// A human wrote this and happened to like the name.\nconst AAProbe = \"authored\"\n")

	// refused installs the fixture, plants the authored bytes, and proves the
	// tree is in the refusing state before any remedy is applied.
	refused := func(t *testing.T) (string, *modkit.Engine) {
		t.Helper()
		root, engine := cliProject(t)
		if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
			t.Fatalf("sync: %v", err)
		}
		writeTestFile(t, root, target, authored)
		if _, _, err := runApp(t, root, engine, "sync", "--offline"); err == nil || exitOf(t, err) != 3 {
			t.Fatalf("the fixture is not in the refusing state: %v", err)
		}
		return root, engine
	}

	t.Run("delete it", func(t *testing.T) {
		root, engine := refused(t)
		if err := os.Remove(filepath.Join(root, target)); err != nil {
			t.Fatal(err)
		}
		if _, errOut, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
			t.Fatalf("deleting the file did not clear the refusal: %v\n%s", err, errOut)
		}
	})

	t.Run("rename it to a name this pipeline does not generate", func(t *testing.T) {
		root, engine := refused(t)
		const renamed = "internal/modules/aa_probe_authored.go"
		if modkit.IsGeneratedOutputPath(renamed) {
			t.Fatalf("%s is still a generated name; the remedy would not clear anything", renamed)
		}
		if err := os.Rename(filepath.Join(root, target), filepath.Join(root, renamed)); err != nil {
			t.Fatal(err)
		}
		if _, errOut, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
			t.Fatalf("renaming off the generated name did not clear the refusal: %v\n%s", err, errOut)
		}
		// The whole point of renaming rather than deleting: the bytes survive.
		if got := readTestFile(t, root, renamed); !bytes.Equal(got, authored) {
			t.Fatalf("the renamed file was not left alone:\n%s", got)
		}
	})

	t.Run("restore the marker if ggg wrote it", func(t *testing.T) {
		root, engine := refused(t)
		restored := append([]byte("// Code generated "+modkit.GeneratedOutputMarker+".\n\n"), authored...)
		writeTestFile(t, root, target, restored)
		out, errOut, err := runApp(t, root, engine, "sync", "--offline")
		if err != nil {
			t.Fatalf("restoring the marker did not clear the refusal: %v\n%s", err, errOut)
		}
		// Restoring the banner is a claim of authorship, so the file rejoins
		// the swept class — named, then removed.
		if !strings.Contains(out, target) {
			t.Fatalf("the re-marked aggregate was swept without being named:\n%s", out)
		}
		if _, statErr := os.Stat(filepath.Join(root, target)); !errors.Is(statErr, fs.ErrNotExist) {
			t.Fatalf("the re-marked aggregate was not swept: %v", statErr)
		}
	})

	// The remedy the message used to lead with. It trades one exit 3 for
	// another and leaves the file exactly where it was.
	t.Run("declaring it in a module's files is refused, not a remedy", func(t *testing.T) {
		files := fixtureRegistry(t)
		payload := []byte("package modules\n\nconst AAProbe = \"declared\"\n")
		probe := baseModule("ggg/element/probe", "element", "probe")
		probe.Files = []modkit.ManifestFile{{
			Source: "registry/modules/element/probe/probe.go", Target: target,
			Class: modkit.FileClassGo, SHA256: sha256Hex(payload), Contract: true,
		}}
		putJSON(t, files, "registry/modules/element/probe/module.json", modkit.ModuleDocument{Schema: 2, Module: probe})
		files["registry/modules/element/probe/probe.go"] = &fstest.MapFile{Data: payload}
		putJSON(t, files, "registry/elements.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogElement, Items: []string{
			"registry/modules/element/button/module.json", "registry/modules/element/probe/module.json",
		}})
		source := refSource{snapshots: map[string]modkit.Snapshot{
			"main": {Commit: testCommitA, FS: files}, testCommitA: {Commit: testCommitA, FS: files},
		}}
		root := t.TempDir()
		writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
		engine := modkit.New(modkit.Options{Source: source, Generator: modkit.RegistryGenerator{}})
		if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
			t.Fatalf("init: %v", err)
		}
		writeTestFile(t, root, target, authored)

		out, errOut, err := runApp(t, root, engine, "add", "ggg/element/probe")
		if err == nil {
			t.Fatalf("a module declared a generated target and it was accepted:\n%s%s", out, errOut)
		}
		if !strings.Contains(out+errOut, "generated outputs are tool-owned and cannot be authored") {
			t.Fatalf("the declare path refused for some other reason:\n%s%s", out, errOut)
		}
		if _, statErr := os.Stat(filepath.Join(root, target)); statErr != nil {
			t.Fatalf("the refused declare removed the file: %v", statErr)
		}
	})

	// `--claim` is the tool's one mechanism for adopting a pre-existing file,
	// and it does not reach this either: a claim adopts a divergent file
	// against a DECLARED target, and declaring is what is refused.
	t.Run("--claim does not adopt it either", func(t *testing.T) {
		root, engine := refused(t)
		out, errOut, err := runApp(t, root, engine, "sync", "--offline", "--claim", target)
		if err == nil || exitOf(t, err) != 3 {
			t.Fatalf("--claim over a generated name = %v, want the same exit 3\n%s%s", err, out, errOut)
		}
		if _, statErr := os.Stat(filepath.Join(root, target)); statErr != nil {
			t.Fatalf("the refused claim removed the file: %v", statErr)
		}
	})
}

// dry-run must never touch the tree: zero writes before apply.
func TestCLIDryRunLeavesIntentUntouched(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	intentPath := filepath.Join(root, "gogogadget.json")
	before, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	// The dry run resolves and reports a real plan — the comment here used to
	// record the opposite ("a dry-run add of a scoped id refuses cleanly"),
	// which was the id defect written down as if it were the contract. The
	// declared exit for a dry run over pending work is 4; anything else
	// (2 invalid operand, 3 unknown id) means the operand was rejected.
	_, _, err = runApp(t, root, engine, "add", "ggg/page/optional", "--dry-run")
	if err == nil || exitOf(t, err) != exitConflict {
		t.Fatalf("add --dry-run = %v, want exit %d", err, exitConflict)
	}
	after, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run rewrote the intent file")
	}
}

// doctor reports on an uninitialized project rather than crashing.
func TestCLIDoctorReportsMissingProject(t *testing.T) {
	_, engine := cliProject(t)
	// Doctor reports on an existing but uninitialized directory.
	out, _, err := runApp(t, t.TempDir(), engine, "doctor", "--json")
	if err != nil && exitOf(t, err) != 3 {
		t.Fatalf("doctor on missing project exit = %d, want 3", exitOf(t, err))
	}
	if !strings.Contains(out, "\"ok\"") {
		t.Fatalf("doctor emitted no envelope: %s", out)
	}
}

// catalog lists the registry without a project; --kind narrows it.
func TestCLICatalogListsRegistry(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "catalog", "--kind", "component", "--json")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if !strings.Contains(out, "component/") || strings.Contains(out, "\"element/") {
		t.Fatalf("catalog --kind component = %s", out)
	}
}

// info returns the per-module contract an agent reads before editing.
func TestCLIInfoReportsModuleContract(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "info", "ggg/component/card", "--json")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	for _, want := range []string{"ggg/component/card", "requires", "files", "removal_policy", "links", "verify", "state", "module"} {
		if !strings.Contains(out, want) {
			t.Fatalf("info output missing %q: %s", want, out)
		}
	}
}

// The self-hosting resolution: a tree with its own registry.json resolves from
// its own tree, never the network.
func TestCLIResolvesSelfHostedRegistryFromTree(t *testing.T) {
	root := t.TempDir()
	fs := fixtureRegistry(t)
	for name, file := range fs {
		writeTestFile(t, root, name, file.Data)
	}
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	if _, _, err := runApp(t, root, nil, "registry", "build"); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	if _, _, err := runApp(t, root, nil, "init", "--adopt", "--offline"); err != nil {
		t.Fatalf("init --adopt --offline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, modkit.LockFileName)); err != nil {
		t.Fatalf("self-hosted adopt wrote no lock: %v", err)
	}
}

// A divergent pre-existing file must refuse with exit 3; --claim adopts the
// operator's bytes untouched and records them as modified.
func TestCLISyncClaimsDivergentFileDuringAdoption(t *testing.T) {
	root, engine := cliProject(t)
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: testKeyA}},
		Providers:  map[string]modkit.ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/page/optional"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, intent)
	local := []byte("package optional\n\n// mine\nconst Version = 99\n")
	writeTestFile(t, root, "internal/modules/optional.go", local)

	_, _, err = runApp(t, root, engine, "sync", "--offline")
	if err == nil || exitOf(t, err) != 3 {
		t.Fatalf("unclaimed adoption = %v, want exit 3", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--check", "--offline", "--claim", "internal/modules/optional.go"); err == nil || exitOf(t, err) != 2 {
		t.Fatalf("sync --check --claim = %v, want usage failure", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--offline", "--claim", "internal/modules/optional.go"); err != nil {
		t.Fatalf("sync --claim: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "internal", "modules", "optional.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, local) {
		t.Fatal("claimed file was rewritten")
	}
	lock, err := modkit.ParseLock([]byte(readFile(t, filepath.Join(root, modkit.LockFileName))))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			if file.Path != "internal/modules/optional.go" {
				continue
			}
			found = true
			if file.State != modkit.FileModified {
				t.Fatalf("claimed file state = %q, want %q", file.State, modkit.FileModified)
			}
			if file.BaseSHA256 == file.LocalSHA256 {
				t.Fatal("base digest was not recorded")
			}
		}
	}
	if !found {
		t.Fatal("claimed file is absent from the lock")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// help and completions are derived from the command table, including the
// contributed ui command.
func TestHelpAndCompletionsDerivedFromTable(t *testing.T) {
	var out bytes.Buffer
	app := App{Out: &out, Contributed: []ContributedCommand{{Spec: CommandSpec{Name: "ui", Summary: "Open the console", Usage: "ggg ui"}}}}
	if err := app.Run(context.Background(), []string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"version", "sync", "ui", "Open the console"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q: %s", want, out.String())
		}
	}
	out.Reset()
	if err := app.Run(context.Background(), []string{"help", "sync"}); err != nil {
		t.Fatalf("help sync: %v", err)
	}
	if !strings.Contains(out.String(), "--check") {
		t.Fatalf("help sync missing flags: %s", out.String())
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out.Reset()
		if err := app.Run(context.Background(), []string{"completion", shell}); err != nil {
			t.Fatalf("completion %s: %v", shell, err)
		}
		if !strings.Contains(out.String(), "ggg") {
			t.Fatalf("completion %s is empty", shell)
		}
	}
}

// The sealed HelpRequest/CompletionRequest on Controller.Execute and the
// `help`/`completion` handlers must render the same table. One build serves
// one command set: the same request may not yield different text depending
// on the entry point.
func TestExecuteHelpMatchesHandlerTable(t *testing.T) {
	contributed := []ContributedCommand{{Spec: CommandSpec{Name: "ui", Summary: "Open the console", Usage: "ggg ui"}}}
	table, conflicts := commandTable(contributed)
	if len(conflicts) != 0 {
		t.Fatalf("commandTable conflicts: %v", conflicts)
	}
	controller := NewController(ControllerOptions{Table: table})

	helpResult, err := controller.Execute(context.Background(), HelpRequest{})
	if err != nil {
		t.Fatalf("Execute help: %v", err)
	}
	helpText, _ := helpResult.Payload["text"].(string)
	completionResult, err := controller.Execute(context.Background(), CompletionRequest{Shell: "bash"})
	if err != nil {
		t.Fatalf("Execute completion: %v", err)
	}
	completionText, _ := completionResult.Payload["text"].(string)

	var appOut bytes.Buffer
	app := App{Out: &appOut, Contributed: contributed}
	if err := app.Run(context.Background(), []string{"help"}); err != nil {
		t.Fatalf("handler help: %v", err)
	}
	handlerHelp := appOut.String()
	appOut.Reset()
	if err := app.Run(context.Background(), []string{"completion", "bash"}); err != nil {
		t.Fatalf("handler completion: %v", err)
	}
	handlerCompletion := appOut.String()

	if handlerHelp != helpText {
		t.Fatalf("Execute help diverges from the handler path:\nexecute:\n%s\nhandler:\n%s", helpText, handlerHelp)
	}
	if handlerCompletion != completionText {
		t.Fatalf("Execute completion diverges from the handler path:\nexecute:\n%s\nhandler:\n%s", completionText, handlerCompletion)
	}
	if !strings.Contains(helpText, "Open the console") {
		t.Fatalf("contributed command missing from help: %s", helpText)
	}
	if !strings.Contains(completionText, "ui") {
		t.Fatalf("contributed command missing from completion: %s", completionText)
	}
}

// No-arg without a terminal is the declared usage failure, never a UI.
func TestNoArgNonTTYIsInteractiveTerminalRequired(t *testing.T) {
	_, _, err := runApp(t, t.TempDir(), nil)
	if err == nil || exitOf(t, err) != 2 {
		t.Fatalf("no-arg non-TTY = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "interactive_terminal_required") {
		t.Fatalf("no-arg non-TTY message = %v", err)
	}
}

// `ui` contributed on a non-TTY stream exits 2 with the same refusal. The
// handler is gggcli/ui.Run via a stub, because the nonvisual tests must not
// import the Charm stack.
func TestUICommandNonTTYRefuses(t *testing.T) {
	app := App{Out: &bytes.Buffer{}, Contributed: []ContributedCommand{{
		Spec: CommandSpec{Name: "ui", Summary: "console"},
		Handler: func(_ context.Context, cc CommandContext, _ []string) (Result, error) {
			if !cc.Interactive {
				return Result{}, usageError("interactive_terminal_required")
			}
			return Result{}, nil
		},
	}}}
	_, _, err := runAppWith(t, app, "ui")
	if err == nil || exitOf(t, err) != 2 {
		t.Fatalf("ui non-TTY = %v, want exit 2", err)
	}
}

// A contributed command that collides with a reserved built-in name is a
// diagnostic, not a crash: it is skipped with a warning and every other
// command still serves.
func TestContributedNameCollisionSkipsWithDiagnostic(t *testing.T) {
	invoked := false
	app := App{Out: &bytes.Buffer{}, Contributed: []ContributedCommand{
		{Spec: CommandSpec{Name: "sync", Summary: "reserved collision"}, Handler: func(context.Context, CommandContext, []string) (Result, error) { return Result{}, nil }},
		{Spec: CommandSpec{Name: "custom", Summary: "a real contribution"}, Handler: func(context.Context, CommandContext, []string) (Result, error) { invoked = true; return Result{}, nil }},
	}}
	_, errOut, err := runAppWith(t, app, "custom")
	if err != nil {
		t.Fatalf("custom command failed: %v", err)
	}
	if !invoked {
		t.Fatal("the non-colliding contributed command did not run")
	}
	if !strings.Contains(errOut, `"sync" collides with a reserved built-in name`) {
		t.Fatalf("no collision diagnostic on stderr: %q", errOut)
	}
	table, conflicts := commandTable(app.Contributed)
	if len(conflicts) != 1 || conflicts[0] != "sync" {
		t.Fatalf("conflicts = %v, want [sync]", conflicts)
	}
	if _, found := lookupSpec(table, "sync"); !found {
		t.Fatal("the reserved built-in sync must still resolve")
	}
}

// Cancelled after preview is the declared user_cancelled refusal (exit 3);
// cancelled before any plan is a clean exit 0.
func TestCancellationContract(t *testing.T) {
	if code := ExitCode(ErrCancelled); code != 0 {
		t.Fatalf("cancel before plan exit = %d, want 0", code)
	}
	err := UserCancelledError{Command: "sync"}
	if code := ExitCode(err); code != 3 {
		t.Fatalf("cancel after preview exit = %d, want 3", code)
	}
	var user UserCancelledError
	if !errors.As(err, &user) || user.Command != "sync" {
		t.Fatalf("error = %#v, want UserCancelledError{sync}", err)
	}
}

func runAppWith(t *testing.T, app App, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytesBuffer
	app.Out, app.Err = &out, &errOut
	err := app.Run(context.Background(), args)
	return out.String(), errOut.String(), err
}

// A planning refusal under --json must still emit the fixed ten-key envelope
// with the command_failed diagnostic and the declared exit code: a machine
// consumer parsing nothing on failure is a broken contract.
func TestSyncPlanningRefusalEmitsJSONEnvelope(t *testing.T) {
	root, engine := cliProject(t)
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: testKeyA}},
		Providers:  map[string]modkit.ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/page/does-not-exist"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, intent)

	out, _, err := runApp(t, root, engine, "sync", "--json")
	if err == nil || exitOf(t, err) != 3 {
		t.Fatalf("sync planning refusal = %v, want exit 3", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("refusal is not JSON: %v\n%s", err, out)
	}
	want := []string{"ok", "command", "run_id", "registry_commit", "resolved", "changes", "generated", "conflicts", "diagnostics", "exit"}
	if len(envelope) != len(want) {
		t.Fatalf("envelope has %d keys, want exactly %d: %s", len(envelope), len(want), out)
	}
	for _, key := range want {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("envelope missing %q: %s", key, out)
		}
	}
	if envelope["ok"] != false || envelope["exit"] != float64(3) || envelope["command"] != "sync" {
		t.Fatalf("envelope = %#v", envelope)
	}
	diagnostics, ok := envelope["diagnostics"].([]any)
	if !ok || len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want one command_failed entry", envelope["diagnostics"])
	}
	first, _ := diagnostics[0].(map[string]any)
	if first["code"] != "command_failed" || first["severity"] != "error" {
		t.Fatalf("diagnostic = %#v", first)
	}
}

// The stale-engine guard has to be reachable from the real command surface,
// because that is where it failed: a `bin/ggg` built before the runtime.health
// capability existed read the new manifests and reported a missing provider on
// a healthy tree. Every lock-reading command must instead refuse with exit 3
// and name the rebuild, and the refusal must land before any write.
func TestCLIRefusesALockWrittenByANewerEngine(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	lockPath := filepath.Join(root, modkit.LockFileName)
	current := readFile(t, lockPath)
	stamped := fmt.Sprintf("%q: %d,", "engine_contract", modkit.EngineContract)
	if !strings.Contains(current, stamped) {
		t.Fatalf("sync did not record %s in the lock:\n%s", stamped, current)
	}
	newer := strings.Replace(current, stamped,
		fmt.Sprintf("%q: %d,", "engine_contract", modkit.EngineContract+1), 1)
	writeTestFile(t, root, modkit.LockFileName, []byte(newer))
	before := treeDigest(t, root)

	for _, args := range [][]string{
		{"sync", "--offline"},
		{"sync", "--check", "--offline"},
		{"diff"},
		{"catalog"},
		{"doctor"},
		// A remote command reaches the lock through its own reader, whose
		// caller labels planning failures runtime errors.
		{"provider", "list"},
	} {
		out, errOut, err := runApp(t, root, engine, args...)
		if err == nil {
			t.Fatalf("%v = nil error, want a stale-engine refusal", args)
		}
		if got := ExitCode(err); got != exitRefusal {
			t.Fatalf("%v exit = %d, want %d (%v)", args, got, exitRefusal, err)
		}
		// What the operator actually sees: the error main prints plus whatever
		// the command rendered. doctor reports through its findings, so both
		// channels count as reachable.
		surface := err.Error() + out + errOut
		for _, want := range []string{modkit.LockFileName, "go build -o bin/ggg ./cmd/ggg"} {
			if !strings.Contains(surface, want) {
				t.Fatalf("%v surface %q is missing %q", args, surface, want)
			}
		}
	}

	// The trusted tasks behind `make setup` and `make check` refuse at preview,
	// so a stale engine writes nothing at all — not even the per-environment
	// env files apply creates before its first lock read. This is the exact
	// path that misreported the tree when `bin/ggg` was stale.
	tasks := NewController(ControllerOptions{Root: root, Engine: engine, TaskRunner: &recordingTaskRunner{}})
	for _, task := range []TaskMutation{{Task: "setup"}, {Task: "check"}, {Task: "generate"}} {
		_, err := tasks.Preview(context.Background(), task)
		if err == nil {
			t.Fatalf("preview %q on a newer lock = nil error, want a stale-engine refusal", task.Task)
		}
		if got := ExitCode(err); got != exitRefusal {
			t.Fatalf("preview %q exit = %d, want %d (%v)", task.Task, got, exitRefusal, err)
		}
		if !strings.Contains(err.Error(), "go build -o bin/ggg ./cmd/ggg") {
			t.Fatalf("preview %q error %q is missing the remedy", task.Task, err)
		}
	}
	if after := treeDigest(t, root); after != before {
		t.Fatal("a refused command wrote to the tree")
	}

	// Rebuilding is the remedy, and the reverse direction is the normal upgrade
	// order: this binary reads an older lock silently and re-stamps it.
	older := strings.Replace(current, stamped, fmt.Sprintf("%q: %d,", "engine_contract", 1), 1)
	writeTestFile(t, root, modkit.LockFileName, []byte(older))
	_, errOut, err := runApp(t, root, engine, "diff")
	if err != nil {
		t.Fatalf("diff against an older lock: %v", err)
	}
	if strings.Contains(errOut, "engine contract") {
		t.Fatalf("an older lock warned: %q", errOut)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
		t.Fatalf("sync against an older lock: %v", err)
	}
	if got := readFile(t, lockPath); !strings.Contains(got, stamped) {
		t.Fatalf("sync did not re-stamp %s:\n%s", stamped, got)
	}
}

// The refusal stays exit 3 whichever layer reports it. Lock readers sit under
// layers that relabel failures — usage for malformed input, runtime for a
// failed task step — and a `sync` that exits 1 sends the operator looking for
// a runtime fault instead of rebuilding the binary.
func TestStaleEngineRefusalOutranksItsReporter(t *testing.T) {
	stale := modkit.EngineContractError{Lock: modkit.EngineContract + 1, Binary: modkit.EngineContract}
	reported := map[string]error{
		"bare":                    stale,
		"runtime-wrapped step":    runtimeError(fmt.Errorf("install declared tools: %w", stale)),
		"planner failure":         plannerFailure{runtimeError(stale)},
		"refusal (already right)": refusalError(stale),
	}
	for name, err := range reported {
		if got := ExitCode(err); got != exitRefusal {
			t.Fatalf("%s exit = %d, want %d", name, got, exitRefusal)
		}
	}
}

// TestCLIDocumentedLoopInstallsAndRemovesWhatTheCatalogPrints drives the
// documented loop — `catalog`, then `add`, `remove` and a targeted `update` —
// through the CLI, with the module id READ OUT OF `catalog`'s own output
// rather than written as a literal.
//
// Nothing in this repository did that, and the cost was `ggg add` shipping in
// v0.11.0 unable to install anything in any id form. `ggg registry validate`
// installs, compiles and removes whole closures, but it does so THROUGH THE
// ENGINE, so the engine was covered while every verb an operator types was
// broken: the catalog printed `ggg/element/button` and the operand validator
// demanded `element/button`, so the scoped form refused at exit 2 as invalid
// and the unscoped form refused at exit 3 as an unknown catalog id.
//
// Reading the id from the catalog is the part that makes this a surface test.
// A literal in a test file can agree with a validator that disagrees with what
// the tool prints, which is exactly how two id forms coexisted for a release.
func TestCLIDocumentedLoopInstallsAndRemovesWhatTheCatalogPrints(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, _, err := runApp(t, root, engine, "catalog", "--json")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	var listed struct {
		Modules []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("catalog --json is not parseable: %v\n%s", err, out)
	}
	printed := ""
	for _, entry := range listed.Modules {
		if entry.Kind == "page" {
			printed = entry.ID
			break
		}
	}
	if printed == "" {
		t.Fatalf("catalog printed no page module: %s", out)
	}
	if err := modkit.ValidateScopedProjectModuleID(printed); err != nil {
		t.Fatalf("catalog printed %q, which is not the canonical scoped form %s: %v",
			printed, modkit.CanonicalModuleIDForm, err)
	}
	// The convenience form an operator may type instead, derived from the same
	// printed id so the two can never drift apart in this test.
	parts := strings.Split(printed, "/")
	unscoped := parts[1] + "/" + parts[2]

	// add, in the form the catalog printed.
	if _, _, err := runApp(t, root, engine, "add", printed); err != nil {
		t.Fatalf("add %s: %v", printed, err)
	}
	installed := installedIDsFromLock(t, root)
	if !slices.Contains(installed, printed) {
		t.Fatalf("add %s did not install it; lock holds %v", printed, installed)
	}

	// remove, in the convenience form, which must resolve to the same module.
	if _, _, err := runApp(t, root, engine, "remove", unscoped); err != nil {
		t.Fatalf("remove %s: %v", unscoped, err)
	}
	if installed := installedIDsFromLock(t, root); slices.Contains(installed, printed) {
		t.Fatalf("remove %s left it installed; lock holds %v", unscoped, installed)
	}

	// The removed module keeps one lock row: a tombstone carrying its retained
	// migration ledger, with no files. That row is not an installed module,
	// and every verb that resolves an operand against the lock has to say so
	// with one voice. `diff` built its resolvable set from every row, so a
	// tombstoned id resolved, matched a row with no files, and printed an
	// empty report at exit 0 while `remove` refused the same id at exit 3 —
	// the silent half-fail for the one id class where it survived.
	if tombstoned := tombstonedIDsFromLock(t, root); !slices.Contains(tombstoned, printed) {
		t.Fatalf("remove %s left no tombstone for %s; lock tombstones %v", unscoped, printed, tombstoned)
	}
	for _, verb := range []string{"diff", "remove"} {
		out, _, err := runApp(t, root, engine, verb, printed)
		if err == nil {
			t.Fatalf("%s %s answered at exit 0 for a tombstoned id: %q", verb, printed, out)
		}
		if code := exitOf(t, err); code != 3 {
			t.Fatalf("%s %s exit = %d, want 3", verb, printed, code)
		}
		if !strings.Contains(err.Error(), "is not installed") {
			t.Fatalf("%s %s refused with %v, want \"is not installed\"", verb, printed, err)
		}
	}

	// add again, in the convenience form.
	if _, _, err := runApp(t, root, engine, "add", unscoped); err != nil {
		t.Fatalf("add %s: %v", unscoped, err)
	}
	if installed := installedIDsFromLock(t, root); !slices.Contains(installed, printed) {
		t.Fatalf("add %s did not install %s; lock holds %v", unscoped, printed, installed)
	}

	// A targeted update of an installed module plans and applies.
	if _, _, err := runApp(t, root, engine, "update", printed); err != nil {
		t.Fatalf("update %s: %v", printed, err)
	}

	// And an id that names nothing refuses rather than half-succeeding.
	for _, absent := range []string{"ggg/element/nope", "element/nope"} {
		if _, _, err := runApp(t, root, engine, "add", absent); err == nil {
			t.Fatalf("add %s succeeded", absent)
		}
	}
}

// TestCLIBothIDFormsSelectTheSameProfile pins the kind list a typed id is
// checked against, for the one kind where the two forms disagreed.
//
// A profile is selectable — `ggg add ggg/profile/full` is as legal as a single
// module, and CatalogSelectableIDs publishes profiles for exactly that reason
// — but the two syntax branches asked two lists. The scoped branch hard-coded
// six kinds including `profile`; the unscoped branch asked validModuleKind,
// which names the five MANIFEST kinds. So `add ggg/profile/full` planned at
// exit 0 while `add profile/full` refused at exit 2 with `module kind
// "profile" is invalid`, and the resolution layer that was ready for it never
// ran.
//
// The assertion is indistinguishability, not two exit codes: both operands
// name one profile, so both must leave the same intent and the same installed
// graph, with the intent holding the canonical scoped form either way.
func TestCLIBothIDFormsSelectTheSameProfile(t *testing.T) {
	const canonical = "ggg/profile/full"
	intents := map[string]string{}
	installs := map[string]string{}
	for _, operand := range []string{canonical, "profile/full"} {
		root, engine := cliProject(t)
		if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
			t.Fatalf("init: %v", err)
		}
		if _, _, err := runApp(t, root, engine, "add", operand); err != nil {
			t.Fatalf("add %s: %v", operand, err)
		}
		intent, err := os.ReadFile(filepath.Join(root, "gogogadget.json"))
		if err != nil {
			t.Fatalf("read intent after add %s: %v", operand, err)
		}
		if !strings.Contains(string(intent), `"`+canonical+`"`) {
			t.Fatalf("add %s recorded %s, not the canonical %s", operand, intent, canonical)
		}
		intents[operand] = string(intent)
		installs[operand] = strings.Join(installedIDsFromLock(t, root), ",")
	}
	if intents[canonical] != intents["profile/full"] {
		t.Fatalf("the two id forms wrote different intent:\n%s\n%s", intents[canonical], intents["profile/full"])
	}
	if installs[canonical] != installs["profile/full"] {
		t.Fatalf("the two id forms installed different graphs:\n%s\n%s", installs[canonical], installs["profile/full"])
	}
}

// TestCLITargetedUpdateSeesTheWholeGraphsSource pins the input scope of the
// whole-graph validators the planner runs.
//
// A targeted update keeps every module outside the advanced closure at its
// locked manifest, and it used to read payload bytes for the closure ALONE
// while still running all twelve static invariants over every manifest. So a
// declaration that legitimately points into another module's package — a shell
// slot renderer, which cannot declare a requirement on the module that owns
// the shell — resolved against a file map that did not contain it, and any
// targeted update refused on an unrelated module. In this repository
// `ggg update ggg/element/avatar` reported that ggg/system/analytics-posthog
// named a renderer "no installed payload in that package declares".
//
// The fixture is the exact shape: `guest` declares the slot and `host` owns
// the package the renderer lives in, with no requirement between them, so
// updating `guest` retains `host`.
func TestCLITargetedUpdateSeesTheWholeGraphsSource(t *testing.T) {
	fsys := slotFixtureRegistry(t)
	source := refSource{snapshots: map[string]modkit.Snapshot{
		"main":      {Commit: testCommitA, FS: fsys},
		testCommitA: {Commit: testCommitA, FS: fsys},
	}}
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	engine := modkit.New(modkit.Options{Source: source, Generator: modkit.RegistryGenerator{}})

	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "add", "ggg/element/host", "ggg/element/guest"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "update", "ggg/element/guest"); err != nil {
		t.Fatalf("targeted update of the slot contributor refused: %v", err)
	}
}

// TestCLITargetedUpdateScansRetainedTreeBytes pins the OTHER half of that
// input scope, the half no test covered.
//
// Ten of the twelve static invariants scan payload bytes for a FORBIDDEN
// pattern, so an absent payload is a scan that never ran. Before the retained
// bytes were merged in they all passed vacuously on every targeted update, and
// only two — shell slot renderers and CSP contribution sources — could refuse
// for an absent file at all. Six of the ten run before the first of those two,
// and a closure declaring neither reaches apply, so the over-reporters were
// never the safety net they looked like.
//
// The fixture is built so that a NARROWING of the scan set fails it, not only
// a removal. `beta` owns a test payload that no declaration anywhere points
// at: no slot, no CSP source, no asset, no adapter, no config field. So the
// obvious optimisation after reading plan.go — "read only retained files whose
// package some declaration names" — drops exactly this file, and the ten scans
// go quiet again with the suite green.
//
// The recorder-handoff scan is the instrument because it needs nothing but a
// module's own declaration that a payload is a Go test: it selects
// class-"test" Go targets and reads them. The payload is published clean and
// SCANNED clean (it builds a recorder and uses it inline), then the tree copy
// gains the goroutine — the same experiment that proved the fix on this
// repository, where a credential-presence selector planted in a retained
// ggg/system/analytics-posthog payload made `update ggg/element/avatar` refuse
// at exit 3 naming file, line and retained module.
func TestCLITargetedUpdateScansRetainedTreeBytes(t *testing.T) {
	fsys := retainedPatternRegistry(t)
	source := refSource{snapshots: map[string]modkit.Snapshot{
		"main":      {Commit: testCommitA, FS: fsys},
		testCommitA: {Commit: testCommitA, FS: fsys},
	}}
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	engine := modkit.New(modkit.Options{Source: source, Generator: modkit.RegistryGenerator{}})

	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "add", "ggg/element/alpha", "ggg/element/beta"); err != nil {
		t.Fatalf("add: %v", err)
	}
	// A targeted update of the unrelated module retains beta, whose bytes now
	// carry the forbidden shape. Post-apply state is what the invariants are
	// about, and this file is part of it.
	writeTestFile(t, root, betaTestTarget, betaHandoffTestContent)

	out, _, err := runApp(t, root, engine, "update", "ggg/element/alpha")
	if err == nil {
		t.Fatalf("targeted update planned over a retained payload holding a recorder handoff: %q", out)
	}
	if code := exitOf(t, err); code != 3 {
		t.Fatalf("update exit = %d, want 3 (a refusal, not a crash): %v", code, err)
	}
	for _, want := range []string{betaTestTarget, "ggg/element/beta", "ResponseRecorder"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal does not name %q, so it cannot be acted on: %v", want, err)
		}
	}
	if !regexp.MustCompile(regexp.QuoteMeta(betaTestTarget) + `:\d+`).MatchString(err.Error()) {
		t.Fatalf("refusal names no line in %s: %v", betaTestTarget, err)
	}
}

const betaTestTarget = "internal/beta/beta_flow_test.go"

// betaCleanTestContent uses a recorder without handing it anywhere, so the
// published payload passes the same scan that the planted one fails.
var betaCleanTestContent = []byte(`package beta

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBetaServesInline(t *testing.T) {
	recorder := httptest.NewRecorder()
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d", recorder.Code)
	}
}
`)

// betaHandoffTestContent is the same test with the call moved onto a goroutine,
// which is the shape ValidateNoRecorderGoroutineHandoff refuses.
var betaHandoffTestContent = []byte(`package beta

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBetaServesOnAGoroutine(t *testing.T) {
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		close(done)
	}()
	<-done
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("code = %d", recorder.Code)
	}
}
`)

// retainedPatternRegistry publishes two unrelated modules. Updating alpha
// retains beta, and beta's only payload is a Go test that nothing declares
// anything about — no slot, no source function, no asset, no adapter — so the
// only way a plan reads it is by reading every retained target.
func retainedPatternRegistry(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	putJSON(t, files, "registry.json", map[string]any{
		"schema": 2, "namespace": "ggg", "canonical_module": "github.com/gogogadget/gogogadget",
		"includes": []string{
			"registry/elements.json", "registry/components.json", "registry/pages.json",
			"registry/workflows.json", "registry/systems.json", "registry/profiles.json",
		},
	})
	putJSON(t, files, "registry/elements.json", modkit.CatalogIndex{
		Schema: 2, Kind: modkit.CatalogElement,
		Items: []string{
			"registry/modules/element/alpha/module.json",
			"registry/modules/element/beta/module.json",
		},
	})
	for name, kind := range map[string]modkit.CatalogKind{
		"registry/components.json": modkit.CatalogComponent,
		"registry/pages.json":      modkit.CatalogPage,
		"registry/workflows.json":  modkit.CatalogWorkflow,
		"registry/systems.json":    modkit.CatalogSystem,
		"registry/profiles.json":   modkit.CatalogProfile,
	} {
		putJSON(t, files, name, modkit.CatalogIndex{Schema: 2, Kind: kind, Items: []string{}})
	}

	alphaContent := []byte("package alpha\n\nconst Version = 1\n")
	alpha := baseModule("ggg/element/alpha", "element", "alpha")
	alpha.Files = []modkit.ManifestFile{{
		Source: "registry/modules/element/alpha/alpha.go", Target: "internal/alpha/alpha.go",
		Class: modkit.FileClassGo, SHA256: sha256Hex(alphaContent), Contract: true,
	}}
	putJSON(t, files, "registry/modules/element/alpha/module.json", modkit.ModuleDocument{Schema: 2, Module: alpha})
	files[alpha.Files[0].Source] = &fstest.MapFile{Data: alphaContent}

	betaContent := []byte("package beta\n\nconst Version = 1\n")
	beta := baseModule("ggg/element/beta", "element", "beta")
	beta.Files = []modkit.ManifestFile{
		{
			Source: "registry/modules/element/beta/beta.go", Target: "internal/beta/beta.go",
			Class: modkit.FileClassGo, SHA256: sha256Hex(betaContent), Contract: true,
		},
		{
			Source: "registry/modules/element/beta/beta_flow_test.go", Target: betaTestTarget,
			Class: modkit.FileClassTest, SHA256: sha256Hex(betaCleanTestContent),
		},
	}
	putJSON(t, files, "registry/modules/element/beta/module.json", modkit.ModuleDocument{Schema: 2, Module: beta})
	files[beta.Files[0].Source] = &fstest.MapFile{Data: betaContent}
	files[beta.Files[1].Source] = &fstest.MapFile{Data: betaCleanTestContent}
	return files
}

// slotFixtureRegistry publishes two unrelated modules where one declares a
// shell slot renderer that lives in the other's package.
func slotFixtureRegistry(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	putJSON(t, files, "registry.json", map[string]any{
		"schema": 2, "namespace": "ggg", "canonical_module": "github.com/gogogadget/gogogadget",
		"includes": []string{
			"registry/elements.json", "registry/components.json", "registry/pages.json",
			"registry/workflows.json", "registry/systems.json", "registry/profiles.json",
		},
	})
	putJSON(t, files, "registry/elements.json", modkit.CatalogIndex{
		Schema: 2, Kind: modkit.CatalogElement,
		Items: []string{
			"registry/modules/element/guest/module.json",
			"registry/modules/element/host/module.json",
		},
	})
	for name, kind := range map[string]modkit.CatalogKind{
		"registry/components.json": modkit.CatalogComponent,
		"registry/pages.json":      modkit.CatalogPage,
		"registry/workflows.json":  modkit.CatalogWorkflow,
		"registry/systems.json":    modkit.CatalogSystem,
		"registry/profiles.json":   modkit.CatalogProfile,
	} {
		putJSON(t, files, name, modkit.CatalogIndex{Schema: 2, Kind: kind, Items: []string{}})
	}

	hostContent := []byte("package modules\n\nimport (\n\t\"context\"\n\n\t\"github.com/a-h/templ\"\n)\n\n" +
		"func HostSlot(_ context.Context, _ map[string]string) templ.Component { return templ.NopComponent }\n")
	host := baseModule("ggg/element/host", "element", "host")
	host.Files = []modkit.ManifestFile{{
		Source: "registry/modules/element/host/host.go", Target: "internal/modules/host.go",
		Class: modkit.FileClassGo, SHA256: sha256Hex(hostContent), RewriteModule: true, Contract: true,
	}}
	host.Dependencies.Go = []modkit.GoDependency{{Module: "github.com/a-h/templ", Version: "v0.3.1020"}}
	putJSON(t, files, "registry/modules/element/host/module.json", modkit.ModuleDocument{Schema: 2, Module: host})
	files[host.Files[0].Source] = &fstest.MapFile{Data: hostContent}

	guestContent := []byte("package guest\n\nconst Version = 1\n")
	guest := baseModule("ggg/element/guest", "element", "guest")
	guest.Runtime.Slots = []modkit.SlotContribution{{
		ID: "guest-head", Slot: modkit.ShellSlotHead,
		Package: "internal/modules", Renderer: "HostSlot",
	}}
	guest.Files = []modkit.ManifestFile{{
		Source: "registry/modules/element/guest/guest.go", Target: "internal/modules/guest.go",
		Class: modkit.FileClassGo, SHA256: sha256Hex(guestContent), Contract: true,
	}}
	putJSON(t, files, "registry/modules/element/guest/module.json", modkit.ModuleDocument{Schema: 2, Module: guest})
	files[guest.Files[0].Source] = &fstest.MapFile{Data: guestContent}
	return files
}

// lockOf reads the project lock an operator's next command resolves against.
func lockOf(t *testing.T, root string) modkit.Lock {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, modkit.LockFileName))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	lock, err := modkit.ParseLock(data)
	if err != nil {
		t.Fatalf("parse lock: %v", err)
	}
	return lock
}

// installedIDsFromLock is the installed graph as the lock records it. It asks
// modkit rather than re-deciding, because a test that spells "installed" for
// itself can agree with one verb while the tool disagrees across three.
func installedIDsFromLock(t *testing.T, root string) []string {
	t.Helper()
	return modkit.InstalledModuleIDs(lockOf(t, root))
}

// tombstonedIDsFromLock is the complement: rows retained for their immutable
// migration ledger after their authored files were removed.
func tombstonedIDsFromLock(t *testing.T, root string) []string {
	t.Helper()
	ids := []string{}
	for _, module := range lockOf(t, root).Modules {
		if module.Reason == modkit.TombstoneReason {
			ids = append(ids, module.ID)
		}
	}
	return ids
}

// treeDigest hashes every tracked path under root so a "nothing was written"
// assertion covers content, not just the files a test remembered to name.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	sum := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(sum, "%s\x00%x\x00", filepath.ToSlash(rel), sha256.Sum256(data))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}
