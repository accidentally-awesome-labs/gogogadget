package gggcli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The measured incident, in the smallest form that reproduces it: a package
// whose every test skipped. `go test` printed `ok` for four of them, the
// implementer read that as a pass, and nothing in the output distinguished it
// from a package that ran.
//
// Mutation: count only Action=="pass" events, or drop the summary, and a
// suite that ran nothing reports the same bytes as a suite that ran.
func TestAccountGoTestReportsWhatTheOkLineHides(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/audit"},
		event{Action: "run", Package: "example.test/audit", Test: "TestLogWritesRow"},
		event{Action: "skip", Package: "example.test/audit", Test: "TestLogWritesRow"},
		event{Action: "run", Package: "example.test/audit", Test: "TestLogNullColumns"},
		event{Action: "skip", Package: "example.test/audit", Test: "TestLogNullColumns"},
		event{Action: "output", Package: "example.test/audit", Output: "ok  \texample.test/audit\t0.599s\n"},
		event{Action: "pass", Package: "example.test/audit", Elapsed: 0.599},
		event{Action: "start", Package: "example.test/pure"},
		event{Action: "run", Package: "example.test/pure", Test: "TestAdds"},
		event{Action: "pass", Package: "example.test/pure", Test: "TestAdds"},
		event{Action: "pass", Package: "example.test/pure", Elapsed: 0.01},
	)

	progress := &bytes.Buffer{}
	account, err := accountGoTest(strings.NewReader(stream), progress)
	if err != nil {
		t.Fatalf("accounting the event stream: %v", err)
	}
	if account.Passed != 1 || account.Skipped != 2 || account.Failed != 0 {
		t.Fatalf("account = %d passed, %d skipped, %d failed; want 1, 2, 0",
			account.Passed, account.Skipped, account.Failed)
	}
	rendered := progress.String() + account.summary(true)
	for _, want := range []string{
		"NO TEST RAN: all 2 skipped",
		"example.test/audit",
		"tests: 1 passed, 2 skipped, 0 inapplicable, 0 failed across 2 packages",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("gate output does not contain %q:\n%s", want, rendered)
		}
	}
	// A package that really ran must not be decorated with a skip count, or
	// the loud line stops being a signal.
	for _, line := range strings.Split(progress.String(), "\n") {
		if strings.Contains(line, "example.test/pure") && strings.Contains(line, "skipped") {
			t.Fatalf("a package with no skips was reported as skipping: %q", line)
		}
	}
}

// A failing package's own output is the diagnosis, and rendering a summary
// line instead of forwarding it would make the gate less useful than the bare
// `go test` it replaces.
func TestAccountGoTestForwardsAFailingPackagesOutput(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/broken"},
		event{Action: "output", Package: "example.test/broken", Output: "--- FAIL: TestThing (0.00s)\n"},
		event{Action: "output", Package: "example.test/broken", Output: "    thing_test.go:9: want 2, got 3\n"},
		event{Action: "fail", Package: "example.test/broken", Test: "TestThing"},
		event{Action: "fail", Package: "example.test/broken", Elapsed: 0.02},
	)
	progress := &bytes.Buffer{}
	account, err := accountGoTest(strings.NewReader(stream), progress)
	if err != nil {
		t.Fatalf("accounting the event stream: %v", err)
	}
	if account.Failed != 1 {
		t.Fatalf("account = %d failed, want 1", account.Failed)
	}
	for _, want := range []string{"FAIL", "example.test/broken", "want 2, got 3"} {
		if !strings.Contains(progress.String(), want) {
			t.Fatalf("gate output does not contain %q:\n%s", want, progress.String())
		}
	}
}

// The one place a skip cannot be legitimate is the one place nothing was
// checking. CI names TEST_DATABASE_URL and runs a service container that
// answers on it, so a skip there is a test that had what it asked for.
//
// Mutation: return false unconditionally, and CI goes back to accepting a
// suite that skipped its whole integration layer.
func TestSkipsAreForbiddenOnlyWhereNothingCanBeAbsent(t *testing.T) {
	for value, forbidden := range map[string]bool{
		"true": true, "1": true, "TRUE": true, " true ": true,
		"": false, "0": false, "false": false, "no": false,
	} {
		if got := skipsAreForbidden(func(string) string { return value }); got != forbidden {
			t.Fatalf("skipsAreForbidden(CI=%q) = %v, want %v", value, got, forbidden)
		}
	}
}

// The refusal has to name the count and the packages, or it is as
// unactionable as the `ok` it replaces.
func TestForbiddenSkipRefusalNamesTheWorstOffenderFirst(t *testing.T) {
	account := goTestAccount{
		Packages: []*packageAccount{
			{Name: "example.test/small", Skipped: 2, Passed: 1},
			{Name: "example.test/web", Skipped: 295, Passed: 77},
		},
		Passed:  78,
		Skipped: 297,
	}
	refusal := account.forbiddenSkipRefusal()
	if !strings.Contains(refusal, "297 test(s) skipped") {
		t.Fatalf("refusal does not carry the total:\n%s", refusal)
	}
	web, small := strings.Index(refusal, "example.test/web"), strings.Index(refusal, "example.test/small")
	if web < 0 || small < 0 || web > small {
		t.Fatalf("refusal does not lead with the package that hid the most:\n%s", refusal)
	}
}

// The accounted run must never degrade to a plain one. A runner that cannot
// hand over the child's stdout can only produce a skip count of zero over a
// suite whose skips were never read — a fabricated account, which is the
// defect this gate exists to close.
func TestAccountedRunRefusesARunnerThatCannotRead(t *testing.T) {
	controller := NewController(ControllerOptions{Root: t.TempDir(), TaskRunner: &recordingTaskRunner{}})
	err := controller.runAccountedGoTest(context.Background(), t.TempDir(), goTestFlags{})
	if err == nil {
		t.Fatal("an unreadable runner produced an account")
	}
	if !strings.Contains(err.Error(), "guess") {
		t.Fatalf("error = %v, want it to say the account would be a guess", err)
	}
}

// A cached `ok` is an account of a run that did not happen. Go's test cache
// keys on the inputs it can observe, and a database that stopped answering is
// not one of them: measured on the real tree, with the test stack torn down,
// this gate reported "1991 passed, 0 skipped" by replaying the event stream
// of a run made while the stack was up.
//
// Mutation: drop -count=1 and the skip account becomes a statement about
// whichever run the cache happens to hold.
func TestAccountedRunNeverReadsTheTestCache(t *testing.T) {
	for name, flags := range map[string]goTestFlags{
		"plain":      {},
		"race+cover": {Race: true, Cover: true},
	} {
		argv := strings.Join(flags.argv(), " ")
		if !strings.Contains(argv, "-count=1") {
			t.Fatalf("%s argv %q may serve a cached run", name, argv)
		}
		if !strings.Contains(argv, "-json") {
			t.Fatalf("%s argv %q cannot be accounted: skips exist only in the event stream", name, argv)
		}
	}
	raced := strings.Join(goTestFlags{Race: true, Cover: true}.argv(), " ")
	for _, want := range []string{"-race", "-cover"} {
		if !strings.Contains(raced, want) {
			t.Fatalf("argv %q dropped %s; CI depends on it", raced, want)
		}
	}
}

// The floor, and it is this gate's own thesis turned on this gate. Before it,
// a run producing exit 0 and no events rendered
// "tests: 0 passed, 0 skipped, 0 inapplicable, 0 failed across 0 packages"
// and returned nil — `go test ./...` against a tree that matches no packages
// exits 0 with only a warning, which a mid-genesis derivative reaches.
//
// Mutation: drop refuseEmptyRun and a suite that does not exist reports a
// pass.
func TestAccountRefusesARunThatExecutedNothing(t *testing.T) {
	for name, account := range map[string]goTestAccount{
		"no packages at all": {},
		"packages but not one test": {Packages: []*packageAccount{
			{Name: "example.test/a", Result: "skip"},
			{Name: "example.test/b", Result: "skip"},
		}},
	} {
		err := account.refuseEmptyRun()
		if err == nil {
			t.Fatalf("%s reported success", name)
		}
		if got := exitOf(t, err); got != exitRefusal {
			t.Fatalf("%s exit = %d, want the refusal code %d", name, got, exitRefusal)
		}
	}
	// One real test is enough to make the run a run.
	ran := goTestAccount{Packages: []*packageAccount{{Name: "example.test/a", Result: "pass", Passed: 1}}, Passed: 1}
	if err := ran.refuseEmptyRun(); err != nil {
		t.Fatalf("a run that executed one test was refused: %v", err)
	}
}

// The totals line says "tests", so it has to count tests. `go test -json`
// reports a verdict for a parent AND for each subtest, so summing every event
// counts a table once per case plus once for the table — a number that means
// something other than its label, in a gate whose whole subject is numbers
// meaning what they say.
//
// Mutation: count every verdict event and this reads 5 instead of 3.
func TestAccountCountsLeafTestsNotVerdictEvents(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/table"},
		event{Action: "pass", Package: "example.test/table", Test: "TestTable/one"},
		event{Action: "pass", Package: "example.test/table", Test: "TestTable/two"},
		event{Action: "skip", Package: "example.test/table", Test: "TestTable/three"},
		event{Action: "pass", Package: "example.test/table", Test: "TestTable"},
		event{Action: "pass", Package: "example.test/table", Test: "TestAlone"},
		event{Action: "pass", Package: "example.test/table", Elapsed: 0.1},
	)
	account, err := accountGoTest(strings.NewReader(stream), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("accounting: %v", err)
	}
	if account.Passed != 3 || account.Skipped != 1 {
		t.Fatalf("account = %d passed, %d skipped; want 3 passed (two subtests plus TestAlone) and 1 skipped",
			account.Passed, account.Skipped)
	}
}

// A skip that declares itself inapplicable is counted apart and never
// refused. Without it the CI refusal ships a false refusal into every
// derivative: internal/config's derivation tests skip when the project's test
// environment publishes no local Postgres, which is exactly what a derivative
// on a managed database does, and "supply what those tests skip for" is not
// advice anyone there can act on.
//
// Mutation: ignore the marker and a derivative on Neon gets exit 3 for doing
// the supported thing.
func TestInapplicableSkipsAreCountedApartAndNeverRefused(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/cfg"},
		event{Action: "output", Package: "example.test/cfg", Test: "TestDerives",
			Output: "    config_test.go:9: " + InapplicableSkipMarker + " the test environment publishes no local Postgres\n"},
		event{Action: "skip", Package: "example.test/cfg", Test: "TestDerives"},
		event{Action: "output", Package: "example.test/cfg", Test: "TestNeedsDB",
			Output: "    db_test.go:9: no test database server\n"},
		event{Action: "skip", Package: "example.test/cfg", Test: "TestNeedsDB"},
		event{Action: "pass", Package: "example.test/cfg", Test: "TestPure"},
		event{Action: "pass", Package: "example.test/cfg", Elapsed: 0.1},
	)
	account, err := accountGoTest(strings.NewReader(stream), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("accounting: %v", err)
	}
	if account.Inapplicable != 1 || account.Skipped != 1 {
		t.Fatalf("account = %d inapplicable, %d skipped; want 1 and 1", account.Inapplicable, account.Skipped)
	}

	// And an all-inapplicable run must not be refused even where skips are
	// forbidden, while the un-marked skip beside it still is.
	only := goTestAccount{Packages: []*packageAccount{{Name: "example.test/cfg", Passed: 1, Inapplicable: 3}}, Passed: 1, Inapplicable: 3}
	if only.Skipped != 0 {
		t.Fatal("an inapplicable skip leaked into the refusable count")
	}
	if got := only.summary(true); !strings.Contains(got, "3 inapplicable") {
		t.Fatalf("summary %q hides the inapplicable count", got)
	}
	if got := account.forbiddenSkipRefusal(); !strings.Contains(got, InapplicableSkipMarker) {
		t.Fatalf("the refusal %q does not tell a derivative how to declare an inapplicable case", got)
	}
}

// templ's --lazy skips regeneration when the output is newer than the source.
// Adding it for speed would silently reduce the drift gate to exactly the
// mtime comparison the design rejects — and it would fail open, reporting a
// clean tree over a stale artifact.
func TestGenerationStepsNeverRunTemplLazily(t *testing.T) {
	lock := modkit.Lock{GoTools: []string{templGoTool, sqlcGoTool}}
	for _, step := range generationSteps(lock) {
		for _, arg := range step {
			if arg == "--lazy" || arg == "-lazy" {
				t.Fatalf("generation step %q runs templ lazily, which makes the drift gate an mtime check", strings.Join(step, " "))
			}
		}
	}
}

// The generation-drift gate, end to end through the real `check` task: a
// generator that rewrites a committed output is a statement that the output
// did not match its source, and `check` must refuse rather than absorb the
// repair and report success.
//
// This is the second measured incident. The core release order — registry
// build, registry sign, sync --offline, sync --check --offline — completed at
// exit 0 with settings_templ.go still carrying the previous copy, because none
// of those four commands runs a generator.
//
// Mutation: drop the before/after comparison, and `check` regenerates the
// stale file, compiles the fresh one, and prints a pass over a tree that did
// not exist when it started.
func TestCheckRefusesGeneratedOutputItsSourceNoLongerProduces(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/web/templates/settings.templ", []byte("templ Settings() {\n\t<p>new</p>\n}\n"))
	writeTestFile(t, root, "internal/web/templates/settings_templ.go", []byte("// Code generated by templ - DO NOT EDIT.\n// stale\n"))

	runner := &generatingRunner{root: root, writes: map[string]string{
		"internal/web/templates/settings_templ.go": "// Code generated by templ - DO NOT EDIT.\n// fresh\n",
	}}
	controller := NewController(ControllerOptions{Root: root, TaskRunner: runner})
	plan, err := controller.Preview(context.Background(), TaskMutation{Task: "check"})
	if err != nil {
		t.Fatalf("preview check: %v", err)
	}
	result, err := controller.Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("check reported success over generated output its source no longer produces")
	}
	if got := exitOf(t, err); got != exitRefusal {
		t.Fatalf("exit = %d, want the refusal code %d", got, exitRefusal)
	}
	if result.Envelope.Exit != exitRefusal || result.Envelope.OK {
		t.Fatalf("envelope = {ok:%v exit:%d}, want {ok:false exit:%d}", result.Envelope.OK, result.Envelope.Exit, exitRefusal)
	}
	for _, want := range []string{"settings_templ.go", "from internal/web/templates/settings.templ"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not contain %q", err, want)
		}
	}
	// Nothing past the drift gate may run: vet and the suite would both be
	// reporting on a tree the operator has not seen.
	for _, argv := range runner.argvs {
		if joined := strings.Join(argv, " "); strings.Contains(joined, "go vet") || strings.Contains(joined, "go test") {
			t.Fatalf("check continued past the refusal: ran %q", joined)
		}
	}
}

// A clean tree must pass the drift gate, and the gate must not mistake a tree
// full of generated files it never touched for drift.
func TestCheckAcceptsAConsistentGeneratedTree(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/web/templates/settings.templ", []byte("templ Settings() {\n}\n"))
	writeTestFile(t, root, "internal/web/templates/settings_templ.go", []byte("// generated\n"))
	writeTestFile(t, root, "internal/modules/bootstrap_registry_gen.go", []byte("package modules\n"))

	runner := &generatingRunner{root: root}
	controller := NewController(ControllerOptions{Root: root, TaskRunner: runner})
	plan, err := controller.Preview(context.Background(), TaskMutation{Task: "check"})
	if err != nil {
		t.Fatalf("preview check: %v", err)
	}
	if _, err := controller.Apply(context.Background(), plan); err != nil {
		t.Fatalf("check on a consistent tree: %v", err)
	}
	var ran []string
	for _, argv := range runner.argvs {
		ran = append(ran, strings.Join(argv, " "))
	}
	joined := strings.Join(ran, " | ")
	for _, want := range []string{"generate", "sync --check --offline", "go vet ./...", "go test -json -count=1 ./...", "go build ./..."} {
		if !strings.Contains(joined, want) {
			t.Fatalf("check did not run %q; it ran %s", want, joined)
		}
	}
}

// A tree carrying no generated output at all — a fresh checkout of a project
// that has never generated — must not be read as drift in either direction.
func TestGeneratedOutputDigestsCoverTheToolAndAggregateClassesOnly(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "internal/web/templates/page_templ.go", []byte("a\n"))
	writeTestFile(t, root, "internal/modules/bootstrap_registry_gen.go", []byte("b\n"))
	writeTestFile(t, root, "internal/db/sqlc/orgs.sql.go", []byte("c\n"))
	writeTestFile(t, root, "static/app.css", []byte("d\n"))
	writeTestFile(t, root, "internal/web/templates/page.templ", []byte("authored\n"))
	writeTestFile(t, root, "internal/web/server.go", []byte("authored\n"))
	// Ignored trees a generator never writes into. node_modules ships its own
	// generated files, and walking it would make every gate run slower for a
	// count nobody owns.
	writeTestFile(t, root, filepath.Join("e2e", "node_modules", "x", "y_templ.go"), []byte("vendor\n"))
	writeTestFile(t, root, filepath.Join("tmp", "scratch_templ.go"), []byte("scratch\n"))

	digests, err := generatedOutputDigests(root)
	if err != nil {
		t.Fatalf("digesting: %v", err)
	}
	want := []string{
		"internal/web/templates/page_templ.go",
		"internal/modules/bootstrap_registry_gen.go",
		"internal/db/sqlc/orgs.sql.go",
		"static/app.css",
	}
	if len(digests) != len(want) {
		t.Fatalf("digested %d paths, want %d: %v", len(digests), len(want), digests)
	}
	for _, path := range want {
		if digests[path] == "" {
			t.Fatalf("%s was not digested: %v", path, digests)
		}
	}
}

type event struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
	Output  string
}

func events(list ...event) string {
	out := &strings.Builder{}
	for _, e := range list {
		fmt.Fprintf(out, `{"Action":%q,"Package":%q,"Test":%q,"Elapsed":%v,"Output":%q}`+"\n",
			e.Action, e.Package, e.Test, e.Elapsed, e.Output)
	}
	return out.String()
}

// generatingRunner records argv and, when the generate step runs, writes the
// files a generator would. It implements TaskOutputRunner because the
// accounted suite step refuses a runner that cannot hand over stdout.
type generatingRunner struct {
	root   string
	writes map[string]string
	argvs  [][]string
}

func (r *generatingRunner) Run(_ context.Context, _ string, argv []string, _ map[string]string) error {
	r.argvs = append(r.argvs, append([]string(nil), argv...))
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "generate") {
		return nil
	}
	for path, body := range r.writes {
		full := filepath.Join(r.root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (r *generatingRunner) RunOutput(ctx context.Context, root string, argv []string, env map[string]string, out io.Writer) error {
	if err := r.Run(ctx, root, argv, env); err != nil {
		return err
	}
	// One package that passes, so the accounted step has a real stream to
	// read rather than an empty one.
	_, err := io.WriteString(out, events(
		event{Action: "start", Package: "example.test/pkg"},
		event{Action: "pass", Package: "example.test/pkg", Test: "TestPasses"},
		event{Action: "pass", Package: "example.test/pkg", Elapsed: 0.01},
	))
	return err
}

func (r *generatingRunner) Progress() io.Writer { return io.Discard }
