package gggcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// A package with no test files contributes to no total. `go test -json`
// reports it as a package-level skip with no Test field, so the account used
// to grow len(Packages) and say nothing — deleting every _test.go from three
// packages produced a summary line indistinguishable from a healthy run
// except for a number nothing compares against a baseline.
//
// Mutation: drop the Untested arm and the totals line reads "1 passed …
// across 4 packages" over three packages that ran nothing.
func TestAccountNamesPackagesWithNoTestFiles(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/a"},
		event{Action: "pass", Package: "example.test/a", Test: "TestOne"},
		event{Action: "pass", Package: "example.test/a", Elapsed: 0.1},
		event{Action: "start", Package: "example.test/gutted"},
		event{Action: "output", Package: "example.test/gutted", Output: "?   example.test/gutted [no test files]\n"},
		event{Action: "skip", Package: "example.test/gutted"},
	)
	account, err := accountGoTest(strings.NewReader(stream), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("accounting: %v", err)
	}
	if got := account.Untested; len(got) != 1 || got[0] != "example.test/gutted" {
		t.Fatalf("Untested = %v, want [example.test/gutted]", got)
	}
	summary := account.summary(true)
	for _, want := range []string{"1 with no test files", "example.test/gutted"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not carry %q, so a package whose tests were deleted reads as one that ran them", summary, want)
		}
	}
}

// The marker is a self-exemption from the skip refusal that any test may
// write, and the reason is the only part of it a gate can check. A marker
// with nothing after it declares nothing, so it is not a declaration: the
// skip falls back to being refusable and the run is refused by name.
//
// Mutation: treat any marker as a declaration and a suite can exempt itself
// wholesale with six characters and no argument.
func TestAccountRefusesAnInapplicableMarkerWithNoReason(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/cfg"},
		event{Action: "output", Package: "example.test/cfg", Test: "TestBare",
			Output: "    cfg_test.go:9: " + InapplicableSkipMarker + "\n"},
		event{Action: "skip", Package: "example.test/cfg", Test: "TestBare"},
		event{Action: "output", Package: "example.test/cfg", Test: "TestStated",
			Output: "    cfg_test.go:9: " + InapplicableSkipMarker + " no local Postgres here\n"},
		event{Action: "skip", Package: "example.test/cfg", Test: "TestStated"},
		event{Action: "pass", Package: "example.test/cfg", Test: "TestPure"},
		event{Action: "pass", Package: "example.test/cfg", Elapsed: 0.1},
	)
	account, err := accountGoTest(strings.NewReader(stream), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("accounting: %v", err)
	}
	if account.Inapplicable != 1 || account.Skipped != 1 {
		t.Fatalf("account = %d inapplicable, %d skipped; want the stated one counted apart and the bare one refusable",
			account.Inapplicable, account.Skipped)
	}
	if got := account.Unreasoned; len(got) != 1 || got[0] != "example.test/cfg.TestBare" {
		t.Fatalf("Unreasoned = %v, want [example.test/cfg.TestBare]", got)
	}
	refusal := account.unreasonedMarkerRefusal()
	for _, want := range []string{"TestBare", "gave no reason"} {
		if !strings.Contains(refusal, want) {
			t.Fatalf("the refusal %q does not name what is wrong and where", refusal)
		}
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

// The totals line's shape is muscle memory — CI, the loop docs and the
// operator's eye all read `tests: N passed, M skipped, …`. The budget line
// after it is new: the slowest three packages, from the elapsed times the
// event stream already carried, so the next conversation about whether a gate
// is too expensive starts from a printed number instead of a guess.
//
// Mutation: print the three alphabetically, take the fastest, drop the line,
// or let it wander from directly after the totals, and this fails.
func TestSummaryNamesTheSlowestPackages(t *testing.T) {
	stream := events(
		event{Action: "start", Package: "example.test/internal/api"},
		event{Action: "pass", Package: "example.test/internal/api", Test: "TestRoutes"},
		event{Action: "pass", Package: "example.test/internal/api", Elapsed: 3.3},
		event{Action: "start", Package: "example.test/internal/jobs"},
		event{Action: "pass", Package: "example.test/internal/jobs", Test: "TestWorker"},
		event{Action: "pass", Package: "example.test/internal/jobs", Elapsed: 37.6},
		event{Action: "start", Package: "example.test/internal/modkit"},
		event{Action: "pass", Package: "example.test/internal/modkit", Test: "TestResolve"},
		event{Action: "pass", Package: "example.test/internal/modkit", Elapsed: 136.1},
		event{Action: "start", Package: "example.test/internal/web"},
		event{Action: "pass", Package: "example.test/internal/web", Test: "TestHandlers"},
		event{Action: "pass", Package: "example.test/internal/web", Elapsed: 160.4},
	)
	account, err := accountGoTest(strings.NewReader(stream), io.Discard)
	if err != nil {
		t.Fatalf("accounting the event stream: %v", err)
	}
	for _, withPackages := range []bool{true, false} {
		summary := account.summary(withPackages)
		lines := strings.Split(strings.TrimRight(summary, "\n"), "\n")
		if len(lines) < 2 {
			t.Fatalf("summary(withPackages=%v) has %d line(s); the budget line is missing:\n%s", withPackages, len(lines), summary)
		}
		// The totals line keeps the shape everything reads.
		totals := regexp.MustCompile(`^tests: [0-9]+ passed, [0-9]+ skipped, [0-9]+ inapplicable, [0-9]+ failed across [0-9]+ packages$`)
		if !totals.MatchString(lines[0]) {
			t.Fatalf("the totals line changed shape: %q", lines[0])
		}
		// The budget line is directly after it, worst first, and the fourth
		// package stays off it.
		if want := "slowest web 160s modkit 136s jobs 38s"; lines[1] != want {
			t.Fatalf("the budget line is %q, want %q", lines[1], want)
		}
	}
}

// The genesis-sweep trigger, driven both ways plus the two degradations. The
// incident it closes: v0.20.0's first push failed CI on a
// derivative-compile break because the only gate that would have caught it —
// the four-profile genesis sweep — was opt-in and nothing local said a
// payload diff had made it mandatory.
func TestNoUnsweptShippedPayloadDiffPassesCheck(t *testing.T) {
	t.Run("a source-only diff passes", func(t *testing.T) {
		root, check, progress := newSweptRepo(t)
		writeTestFile(t, root, "internal/probe/probe_selfhost_test.go", []byte("package probe\n\n// edited: an assertion about this repository, which never ships\n"))
		if err := check(context.Background(), root); err != nil {
			t.Fatalf("a self_host payload diff was refused:\n%v", err)
		}
		writeTestFile(t, root, "docs/notes.md", []byte("unowned project notes\n"))
		if err := check(context.Background(), root); err != nil {
			t.Fatalf("an unowned-file diff was refused:\n%v", err)
		}
		if reason := progress.String(); strings.Contains(reason, "skipped") {
			t.Fatalf("a runnable trigger reported a skip it had no reason to:\n%s", reason)
		}
	})

	t.Run("a shipped-payload diff refuses, naming the file and the remedy", func(t *testing.T) {
		// The ack env completes a real check, and an ambient one must not
		// hollow out this proof: `GGG_GENESIS_SWEEP=1 make check` runs the
		// suite under the env, and the refusal still has to be exercisable
		// there.
		t.Setenv(genesisSweepEnv, "")
		root, check, _ := newSweptRepo(t)
		writeTestFile(t, root, "internal/probe/probe.go", []byte("package probe\n\nconst Edited = 2\n"))
		err := check(context.Background(), root)
		if err == nil {
			t.Fatal("a diff that edits the shipped payload of an installed module passed the trigger")
		}
		for _, want := range []string{
			"internal/probe/probe.go",
			"payload of ggg/system/probe",
			genesisSweepRemedy,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal does not name %q:\n%v", want, err)
			}
		}
	})

	t.Run("a module-manifest diff refuses as a declaration change", func(t *testing.T) {
		t.Setenv(genesisSweepEnv, "")
		root, check, _ := newSweptRepo(t)
		manifest := sweepFixtureManifest()
		manifest.Revision = 2
		document, marshalErr := json.Marshal(modkit.ModuleDocument{Schema: 2, Module: manifest})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		writeTestFile(t, root, "registry/modules/system/probe/module.json", document)
		err := check(context.Background(), root)
		if err == nil {
			t.Fatal("a diff that edits a module manifest passed the trigger")
		}
		if !strings.Contains(err.Error(), "declaration of ggg/system/probe") {
			t.Fatalf("the refusal does not name the manifest as a declaration change:\n%v", err)
		}
	})

	t.Run("a profile declaration diff refuses, though no profile is installed", func(t *testing.T) {
		t.Setenv(genesisSweepEnv, "")
		root, check, _ := newSweptRepo(t)
		profile := sweepFixtureProfile()
		profile.Revision = 2
		document, marshalErr := json.Marshal(modkit.ProfileDocument{Schema: 2, Profile: profile})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		writeTestFile(t, root, "registry/profiles/probe.json", document)
		err := check(context.Background(), root)
		if err == nil {
			t.Fatal("a diff that edits a profile declaration passed the trigger; the sweep walks catalog.Profiles, and three of the four shipped profiles are installed by nobody")
		}
		if !strings.Contains(err.Error(), "registry/profiles/probe.json") {
			t.Fatalf("the refusal does not name the profile declaration:\n%v", err)
		}
	})

	t.Run("GGG_GENESIS_SWEEP=1 runs the sweep instead of refusing", func(t *testing.T) {
		t.Setenv(genesisSweepEnv, "1")
		root, check, progress := newSweptRepo(t)
		writeTestFile(t, root, "internal/probe/probe.go", []byte("package probe\n\nconst Edited = 2\n"))
		if err := check(context.Background(), root); err != nil {
			t.Fatalf("the ack env is set, so the sweep is this run's concern, but the trigger refused:\n%v", err)
		}
		if note := progress.String(); !strings.Contains(note, "runs inside this check's accounted suite") {
			t.Fatalf("the ack path printed no statement of what it is doing instead:\n%s", note)
		}
	})

	t.Run("no origin degrades to a stated skip", func(t *testing.T) {
		t.Setenv(genesisSweepEnv, "")
		root := t.TempDir()
		writeSweepFixture(t, root)
		sweepGit(t, root, "init", "-b", "main")
		sweepGit(t, root, "add", "-A")
		sweepGit(t, root, "-c", "user.email=probe@example.test", "-c", "user.name=Probe", "commit", "-m", "base")
		writeTestFile(t, root, "internal/probe/probe.go", []byte("package probe\n\nconst Edited = 2\n"))
		controller, progress := sweepController(t, root)
		if err := controller.refuseUnsweptShippedPayloadDiff(context.Background(), root); err != nil {
			t.Fatalf("a tree with no origin was refused — the skip exists so a fresh clone or a closed tree is never blocked:\n%v", err)
		}
		if reason := progress.String(); !strings.Contains(reason, "genesis sweep trigger skipped: no origin/main merge-base") {
			t.Fatalf("the skip did not state its reason:\n%s", reason)
		}
	})

	t.Run("a tree that publishes no catalog skips, stating it", func(t *testing.T) {
		root := t.TempDir()
		lock, err := modkit.MarshalLock(modkit.Lock{
			Schema: 2, RegistryCommit: strings.Repeat("a", 40),
			Order:   []string{sweepFixtureManifest().ID},
			Modules: []modkit.LockedModule{sweepLockedModule(sweepFixtureManifest())},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, modkit.LockFileName, lock)
		_, skipped, derivationErr := shippedPathsForSweep(root)
		if derivationErr != nil {
			t.Fatalf("a derivative-shaped tree errored instead of skipping: %v", derivationErr)
		}
		if !strings.Contains(skipped, "publishes no registry") {
			t.Fatalf("the skip reason %q does not say the tree publishes no registry", skipped)
		}
	})

	t.Run("a collapsed derivation refuses rather than passing vacuously", func(t *testing.T) {
		root := t.TempDir()
		writeSweepFixture(t, root)
		// The manifest still declares its payloads, but every one of them is
		// self_host — the derivation runs, the lock installs modules, and the
		// shipped set comes back empty. A trigger diffing against nothing is
		// green over every payload diff, which is the collapse this floor
		// refuses.
		manifest := sweepFixtureManifest()
		for i := range manifest.Files {
			manifest.Files[i].SelfHost = true
			manifest.Files[i].Class = modkit.FileClassTest
		}
		lock, err := modkit.MarshalLock(modkit.Lock{
			Schema: 2, RegistryCommit: strings.Repeat("a", 40),
			Order:   []string{manifest.ID},
			Modules: []modkit.LockedModule{sweepLockedModule(manifest)},
		})
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, modkit.LockFileName, lock)
		_, _, derivationErr := shippedPathsForSweep(root)
		if derivationErr == nil {
			t.Fatal("a derivation with zero shipped payloads passed; the floor exists so a collapsed set can never be vacuously green")
		}
		if !strings.Contains(derivationErr.Error(), "collapsed") {
			t.Fatalf("the refusal does not name the collapse:\n%v", derivationErr)
		}
	})
}

// sweepFixtureDigest is any valid hex payload digest; the trigger never reads
// payload bytes, only the paths the manifest declares.
const sweepFixtureDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// sweepFixtureManifest is one installed module with two payloads: one that
// ships to derivatives and one that asserts about the publishing repository.
func sweepFixtureManifest() modkit.Manifest {
	return modkit.Manifest{
		ID: "ggg/system/probe", Kind: modkit.ModuleSystem, Name: "probe",
		Revision: 1, Contract: 1, Title: "Probe", Description: "Probe module.",
		Requires:     []modkit.Requirement{},
		Dependencies: modkit.Dependencies{Go: []modkit.GoDependency{}, Tools: []modkit.ToolArtifact{}, Containers: []modkit.ContainerDependency{}},
		Files: []modkit.ManifestFile{
			{Source: "internal/probe/probe.go", Target: "internal/probe/probe.go", Class: modkit.FileClassGo, SHA256: sweepFixtureDigest, RewriteModule: true},
			{Source: "internal/probe/probe_selfhost_test.go", Target: "internal/probe/probe_selfhost_test.go", Class: modkit.FileClassTest, SHA256: sweepFixtureDigest, SelfHost: true},
		},
		Claims:        modkit.NamespaceClaims{},
		Runtime:       modkit.RuntimeContributions{},
		Migrations:    []modkit.ManifestMigration{},
		Environment:   []modkit.EnvironmentVariable{},
		Docs:          []modkit.DocumentationRef{},
		Tests:         modkit.TestMetadata{},
		Data:          []modkit.DataDeclaration{},
		RemovalPolicy: modkit.RemovalFree,
	}
}

// sweepFixtureProfile is a catalog profile nobody installs — the reason the
// declaration set comes from the catalog and not the lock.
func sweepFixtureProfile() modkit.Profile {
	return modkit.Profile{
		ID: "ggg/profile/probe", Kind: modkit.CatalogProfile, Name: "probe",
		Revision: 1, Contract: 1, Title: "Probe", Description: "Probe profile.",
		Members: []string{sweepFixtureManifest().ID}, RequiredProviderSlots: []string{},
		ProviderDefaults: map[string]modkit.ProviderSelections{},
	}
}

// sweepLockedModule locks one manifest with a file row per payload target,
// the shape ParseLock requires: every manifest file target covered exactly.
func sweepLockedModule(manifest modkit.Manifest) modkit.LockedModule {
	files := make([]modkit.LockedFile, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		files = append(files, modkit.LockedFile{
			Path: file.Target, Source: file.Source,
			BaseSHA256: sweepFixtureDigest, LocalSHA256: sweepFixtureDigest,
			State: modkit.FileClean,
		})
	}
	return modkit.LockedModule{
		ID: manifest.ID, Revision: 1, Contract: 1,
		RegistryNamespace: "ggg", SourceCommit: strings.Repeat("b", 40), SnapshotSHA256: strings.Repeat("c", 64),
		Reason: "explicit", RequiredBy: []string{},
		Files: files, Migrations: []modkit.LockedMigration{}, Manifest: manifest,
	}
}

// writeSweepFixture lays down the smallest publishing repository the trigger
// exercises: the payload bytes the manifest declares, a loadable catalog
// (registry.json, all six indexes, one module document, one profile
// document), and the lock that installs the module. The lock is what the
// payload set derives from; the catalog is what the declaration set derives
// from.
func writeSweepFixture(t *testing.T, root string) {
	t.Helper()
	writeTestFile(t, root, "internal/probe/probe.go", []byte("package probe\n\nconst Probe = 1\n"))
	writeTestFile(t, root, "internal/probe/probe_selfhost_test.go", []byte("package probe\n\n// an assertion about this repository only\n"))

	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"example.test/probe","includes":["registry/elements.json","registry/components.json","registry/pages.json","registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, index := range []struct {
		path  string
		kind  string
		items string
	}{
		{"registry/elements.json", "element", "[]"},
		{"registry/components.json", "component", "[]"},
		{"registry/pages.json", "page", "[]"},
		{"registry/workflows.json", "workflow", "[]"},
		{"registry/systems.json", "system", `["registry/modules/system/probe/module.json"]`},
		{"registry/profiles.json", "profile", `["registry/profiles/probe.json"]`},
	} {
		writeTestFile(t, root, index.path, []byte(fmt.Sprintf(`{"schema":2,"kind":%q,"items":%s}`, index.kind, index.items)))
	}
	moduleDocument, err := json.Marshal(modkit.ModuleDocument{Schema: 2, Module: sweepFixtureManifest()})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "registry/modules/system/probe/module.json", moduleDocument)
	profileDocument, err := json.Marshal(modkit.ProfileDocument{Schema: 2, Profile: sweepFixtureProfile()})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "registry/profiles/probe.json", profileDocument)

	manifest := sweepFixtureManifest()
	lock, err := modkit.MarshalLock(modkit.Lock{
		Schema: 2, RegistryCommit: strings.Repeat("a", 40),
		Order:   []string{manifest.ID},
		Modules: []modkit.LockedModule{sweepLockedModule(manifest)},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.LockFileName, lock)
}

// sweepGit runs git in dir and fails the test on any nonzero exit, because a
// fixture that silently failed to commit proves nothing about the diff.
func sweepGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Probe", "GIT_AUTHOR_EMAIL=probe@example.test",
		"GIT_COMMITTER_NAME=Probe", "GIT_COMMITTER_EMAIL=probe@example.test")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// sweepController builds a controller whose task runner captures the progress
// stream, so a skip's stated reason and a note's wording are assertable.
func sweepController(t *testing.T, root string) (*Controller, *bytes.Buffer) {
	t.Helper()
	progress := &bytes.Buffer{}
	return NewController(ControllerOptions{Root: root, TaskRunner: osTaskRunner{out: io.Discard, err: progress}}), progress
}

// newSweptRepo is a committed fixture repository with an origin pushed, so
// the merge-base diff is real: the shipped-payload and declaration subtests
// exercise actual `git diff --name-only` output, not a hand-fed list.
func newSweptRepo(t *testing.T) (string, func(context.Context, string) error, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	writeSweepFixture(t, root)
	sweepGit(t, root, "init", "-b", "main")
	sweepGit(t, root, "add", "-A")
	sweepGit(t, root, "commit", "-m", "base")
	origin := filepath.Join(t.TempDir(), "origin.git")
	sweepGit(t, root, "init", "--bare", "-b", "main", origin)
	sweepGit(t, root, "remote", "add", "origin", origin)
	sweepGit(t, root, "push", "-u", "origin", "main")
	sweepGit(t, root, "fetch", "origin")
	controller, progress := sweepController(t, root)
	return root, controller.refuseUnsweptShippedPayloadDiff, progress
}
