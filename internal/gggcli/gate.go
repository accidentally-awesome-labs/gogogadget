package gggcli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// This file holds the two honesty rules `ggg check` enforces over work it
// reports on. Both exist because a gate reported success over work it had not
// done:
//
//   - `go test` does not summarise skips, so a package whose every fixture
//     skipped prints the same `ok` as one that passed. An entire integration
//     layer skipped against a torn-down test stack, four packages printed
//     `ok`, and that was reported as a pass.
//   - `templ`, `sqlc` and Tailwind own committed output, and nothing refused a
//     compiled artifact that its declared source no longer produced. The core
//     release order — `registry build`, `registry sign`, `sync --offline`,
//     `sync --check --offline` — completed at exit 0 with a `_templ.go` still
//     carrying the previous copy, because none of those four commands runs a
//     generator.

// TaskOutputRunner is TaskRunner for a child whose STDOUT is DATA the caller
// parses rather than progress a human reads.
//
// `go test -json` is the case that forced it. A skipped test exists nowhere in
// plain `go test` output — not in the `ok` line, not in a trailing summary —
// so the only way to count one is to read the event stream, which means
// capturing the child's stdout instead of forwarding it.
type TaskOutputRunner interface {
	// RunOutput streams the child's stdout into out and its stderr wherever
	// Run sends it.
	RunOutput(ctx context.Context, root string, argv []string, env map[string]string, out io.Writer) error
	// Progress is where a task's own rendered output belongs: the same stream
	// its children write to, so a summary and the tool chatter it summarises
	// cannot land on different streams depending on which produced it.
	Progress() io.Writer
}

func (r osTaskRunner) RunOutput(ctx context.Context, root string, argv []string, env map[string]string, out io.Writer) error {
	return osTaskRunner{out: out, err: r.err}.Run(ctx, root, argv, env)
}

func (r osTaskRunner) Progress() io.Writer { return r.err }

// goTestFlags are the extra `go test` flags a caller may add. They are a
// closed set on purpose: the argv of a trusted task is chosen by the handler,
// never assembled from user text.
type goTestFlags struct {
	Race  bool
	Cover bool
}

func (f goTestFlags) argv() []string {
	// -count=1 is load-bearing, not a habit. Go's test cache is keyed on the
	// inputs it can observe — files opened, environment read — and a
	// database that stopped answering is not one of them. So a cached entry
	// replays the PREVIOUS run's event stream, including its skip count:
	// measured, with the test stack torn down and this gate reporting
	// "1991 passed, 0 skipped" from a run made while it was up. An account of
	// a run that did not happen is the defect this file exists to close.
	//
	// It is also what every module's own declared verify command already uses
	// (`ggg info` prints `go test -count=1 ./<pkg>`), so the gate and the
	// per-module instructions now agree.
	//
	// -json first so the stream shape is fixed regardless of what follows.
	argv := []string{"go", "test", "-json", "-count=1"}
	if f.Race {
		argv = append(argv, "-race")
	}
	if f.Cover {
		argv = append(argv, "-cover")
	}
	return append(argv, "./...")
}

// runAccountedGoTest runs the whole suite and accounts for what it actually
// did. It replaces a bare `go test ./...`, whose output cannot distinguish a
// package that passed from a package that skipped everything.
func (c *Controller) runAccountedGoTest(ctx context.Context, root string, flags goTestFlags) error {
	runner := c.runner()
	capable, ok := runner.(TaskOutputRunner)
	if !ok {
		// Falling back to a plain run would report a skip count of zero over
		// a suite whose skips were never read, which is precisely the shape
		// of failure this gate exists to replace.
		return fmt.Errorf("task runner %T cannot read `go test -json`, so the skip account would be a guess", runner)
	}
	progress := capable.Progress()
	argv := flags.argv()

	reader, writer := io.Pipe()
	type outcome struct {
		account goTestAccount
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		account, err := accountGoTest(reader, progress)
		// Draining matters even after a parse error: the child blocks on a
		// full pipe otherwise and the run never ends.
		_, _ = io.Copy(io.Discard, reader)
		done <- outcome{account: account, err: err}
	}()
	runErr := capable.RunOutput(ctx, root, argv, nil, writer)
	_ = writer.Close()
	result := <-done
	_ = reader.Close()

	if result.err != nil {
		return fmt.Errorf("%s: reading the test event stream: %w", strings.Join(argv, " "), result.err)
	}
	// A skip is legitimate for a contributor with no stack and never
	// legitimate where every service the suite asks for is present, so the
	// count is reported always and enforced only where absence is impossible.
	// The per-package breakdown is printed exactly once: by the summary when
	// the skips are allowed, by the refusal when they are not.
	forbidden := result.account.Skipped > 0 && skipsAreForbidden(os.Getenv)
	writeString(progress, result.account.summary(!forbidden))
	if runErr != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv, " "), runErr)
	}
	// The floor, and it is this file's own thesis turned on this file.
	// `go test ./...` against a tree that matches no packages exits 0 with a
	// warning and no events, which this accounting would have rendered as
	// "tests: 0 passed, 0 skipped, 0 failed across 0 packages" and then
	// returned nil — a gate reporting success over a suite that does not
	// exist. Reachable on a mid-genesis derivative, or on a project whose Go
	// packages have not been generated yet.
	if err := result.account.refuseEmptyRun(); err != nil {
		return err
	}
	// A bare marker is refused everywhere, not only where skips are. It is
	// not a skip the environment forced; it is a declaration the test made,
	// and an undeclared reason is the one thing this gate can check about it.
	if len(result.account.Unreasoned) > 0 {
		return refusalError(fmt.Errorf("%s", result.account.unreasonedMarkerRefusal()))
	}
	if forbidden {
		return refusalError(fmt.Errorf("%s", result.account.forbiddenSkipRefusal()))
	}
	return nil
}

// skipsAreForbidden reports whether a skipped test is a gate failure here.
//
// CI provides every service the suite asks for: the workflow's env block
// names TEST_DATABASE_URL and a service container answers on it. A skip there
// is a test that had everything it needed and still did not run, and it is
// indistinguishable in `go test` output from one that passed. Locally the
// same skip is the supported path for a contributor who has never started the
// test stack, so there it is counted and printed rather than refused.
func skipsAreForbidden(lookup func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(lookup("CI"))) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

// InapplicableSkipMarker is how a test declares that it is skipping because
// the case does not APPLY here, rather than because something it needed was
// absent. A skip carrying it is counted separately and never refused.
//
// It exists because the CI refusal would otherwise ship a false refusal into
// every derivative. `internal/config`'s derivation tests skip when the
// project's test environment publishes no local Postgres, and a derivative
// that legitimately selects a managed database (Neon is a first-class target
// here) is in exactly that position: there is nothing to supply and the skip
// is correct, so "supply what those tests skip for, or make the skip a
// failure at its source" is unactionable advice. Without a way to say
// "inapplicable", the only remaining move is to delete the test — which is
// the pressure that has to be relieved, not applied.
//
// It is deliberately a declaration at the skip site and not a heuristic. The
// gate cannot tell an absent service from an inapplicable case by looking;
// only the test knows, so only the test may say. Where the inapplicability is
// known before the subtest starts, NOT REGISTERING the case is still better —
// an unregistered case is a smaller table, while a marked skip is still a
// test that did not run.
const InapplicableSkipMarker = "[inapplicable]"

// goTestAccount is what a suite run actually did, per package and in total.
type goTestAccount struct {
	Packages     []*packageAccount
	Passed       int
	Skipped      int
	Inapplicable int
	Failed       int
	// Untested names the packages that reported no test at all. `go test`
	// prints those as `?   pkg [no test files]` and the totals line said
	// nothing about them, so deleting every _test.go from three packages
	// produced a summary indistinguishable from a healthy run.
	Untested []string
	// Unreasoned names the tests that declared themselves inapplicable and
	// gave no reason. The marker is a self-exemption from the skip refusal;
	// an exemption with no stated reason is a claim nobody can check.
	Unreasoned []string
}

type packageAccount struct {
	Name         string
	Result       string
	Elapsed      float64
	Coverage     string
	Passed       int
	Skipped      int
	Inapplicable int
	Failed       int
	// verdicts is every test name this package reported, with its action.
	// Counting is deferred to tally, at package close, because whether a name
	// is a leaf is only knowable once its siblings have been seen.
	verdicts map[string]string
	// inapplicable is the test names whose own output carried the marker,
	// mapped to whether that marker was followed by a reason.
	inapplicable map[string]bool
	// unreasoned is the tests that wrote the marker with nothing after it.
	unreasoned []string
	output     []string
}

// tally counts LEAF tests. `go test -json` reports a verdict for a parent AND
// for each of its subtests, so summing every event counts a table-driven test
// once per case plus once more for the table itself — the totals line would be
// pass EVENTS under a label that says tests, and this file is not the place to
// print a number that means something other than what it says. A name that is
// the prefix of another name is a parent and is not counted.
func (p *packageAccount) tally() {
	for name, action := range p.verdicts {
		if p.isParent(name) {
			continue
		}
		switch action {
		case "pass":
			p.Passed++
		case "skip":
			if reasoned, declared := p.inapplicable[name]; declared {
				if reasoned {
					p.Inapplicable++
					continue
				}
				// An undeclared reason is not a declaration. It falls back
				// to being an ordinary skip AND is named separately, so the
				// run refuses rather than silently accepting the exemption.
				p.unreasoned = append(p.unreasoned, name)
			}
			p.Skipped++
		case "fail":
			p.Failed++
		}
	}
}

func (p *packageAccount) isParent(name string) bool {
	prefix := name + "/"
	for other := range p.verdicts {
		if strings.HasPrefix(other, prefix) {
			return true
		}
	}
	return false
}

// accountGoTest reads a `go test -json` event stream, renders one line per
// package to progress as each finishes, and returns the counts.
//
// It renders its own per-package line rather than forwarding the stream's
// output events, because `-json` implies verbose: forwarding would replace
// today's one line per package with one line per test. The rendered line
// carries what the `ok` line cannot — how many of the package's tests ran and
// how many skipped — and a FAILING package's captured output is forwarded
// verbatim, so diagnosis loses nothing.
func accountGoTest(events io.Reader, progress io.Writer) (goTestAccount, error) {
	account := goTestAccount{}
	byName := map[string]*packageAccount{}
	scanner := bufio.NewScanner(events)
	// Test output lines can be long (a testify diff of two structs), and the
	// default 64 KiB token limit would abort the parse on one.
	scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			// test2json passes non-event text through only when the child
			// wrote something it could not attribute; it is not an event, and
			// it is not a count.
			continue
		}
		var event struct {
			Action  string  `json:"Action"`
			Package string  `json:"Package"`
			Test    string  `json:"Test"`
			Elapsed float64 `json:"Elapsed"`
			Output  string  `json:"Output"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return account, err
		}
		if event.Package == "" {
			continue
		}
		pkg, ok := byName[event.Package]
		if !ok {
			pkg = &packageAccount{Name: event.Package}
			byName[event.Package] = pkg
			account.Packages = append(account.Packages, pkg)
		}
		switch event.Action {
		case "output":
			if coverage := coverageOf(event.Output); coverage != "" {
				pkg.Coverage = coverage
			}
			// A test's own output is the only channel `testing` gives a skip
			// to say WHY, so the marker is read from there and attributed to
			// the test that wrote it — along with whether it said anything
			// after the marker. A bare marker is a self-exemption from the
			// skip refusal with no claim attached, and the whole point of
			// the marker is that only the test knows the reason.
			if event.Test != "" && strings.Contains(event.Output, InapplicableSkipMarker) {
				if pkg.inapplicable == nil {
					pkg.inapplicable = map[string]bool{}
				}
				_, reason, _ := strings.Cut(event.Output, InapplicableSkipMarker)
				pkg.inapplicable[event.Test] = pkg.inapplicable[event.Test] || strings.TrimSpace(reason) != ""
			}
			pkg.output = append(pkg.output, event.Output)
		case "pass", "fail", "skip":
			if event.Test != "" {
				if pkg.verdicts == nil {
					pkg.verdicts = map[string]string{}
				}
				pkg.verdicts[event.Test] = event.Action
				continue
			}
			pkg.Result = event.Action
			pkg.Elapsed = event.Elapsed
			pkg.tally()
			account.Passed += pkg.Passed
			account.Skipped += pkg.Skipped
			account.Inapplicable += pkg.Inapplicable
			account.Failed += pkg.Failed
			for _, name := range pkg.unreasoned {
				account.Unreasoned = append(account.Unreasoned, pkg.Name+"."+name)
			}
			// `go test -json` reports a package with no test files as a
			// package-level skip carrying no Test field, so nothing above
			// counts it and every total stays 0 while len(Packages) grows.
			if pkg.Passed+pkg.Failed+pkg.Skipped+pkg.Inapplicable == 0 {
				account.Untested = append(account.Untested, pkg.Name)
			}
			writeString(progress, pkg.line())
			if event.Action == "fail" {
				// The failing package's own output IS the diagnosis, so it
				// goes through untouched.
				for _, out := range pkg.output {
					writeString(progress, out)
				}
			}
			pkg.output = nil
			pkg.verdicts = nil
			pkg.inapplicable = nil
			pkg.unreasoned = nil
		}
	}
	return account, scanner.Err()
}

// line is one package's verdict, in the shape `go test` uses so the gate's
// output stays scannable, plus the count `go test` omits.
func (p packageAccount) line() string {
	verdict := "ok  "
	switch p.Result {
	case "fail":
		verdict = "FAIL"
	case "skip":
		// A package-level skip is `go test`'s "no test files".
		verdict = "?   "
	}
	line := fmt.Sprintf("%s\t%s\t%.3fs", verdict, p.Name, p.Elapsed)
	if p.Coverage != "" {
		line += "\t" + p.Coverage
	}
	total := p.Passed + p.Failed + p.Skipped + p.Inapplicable
	switch {
	case p.Skipped == 0 && p.Inapplicable > 0 && p.Passed+p.Failed == 0:
		// Ran nothing, but declared why, so it is not the incident.
		line += fmt.Sprintf("\tno applicable test: all %d inapplicable", p.Inapplicable)
	case p.Skipped+p.Inapplicable > 0 && p.Passed == 0 && p.Failed == 0:
		// The incident, named where it happens: this line used to read `ok`
		// and nothing else.
		line += fmt.Sprintf("\tNO TEST RAN: all %d skipped", p.Skipped+p.Inapplicable)
	case p.Skipped > 0:
		line += fmt.Sprintf("\t%d of %d skipped", p.Skipped, total)
	}
	if p.Inapplicable > 0 && p.Passed+p.Failed > 0 {
		line += fmt.Sprintf("\t%d inapplicable", p.Inapplicable)
	}
	return line + "\n"
}

// summary is the count `go test` never prints. The totals line is emitted on
// every run, pass or fail, because a skip that is legitimate here is still a
// test that did not run and the operator has to be able to see it. The counts
// are LEAF tests, not verdict events — see packageAccount.tally.
//
// withPackages is false when a refusal is about to print the same breakdown,
// so the list appears exactly once.
func (a goTestAccount) summary(withPackages bool) string {
	out := fmt.Sprintf("tests: %d passed, %d skipped, %d inapplicable, %d failed across %d packages",
		a.Passed, a.Skipped, a.Inapplicable, a.Failed, len(a.Packages))
	// The package count moved and nothing said why. `?   pkg [no test files]`
	// is a package that contributed nothing to any total, so a suite whose
	// tests were deleted reads exactly like one that ran them — apart from a
	// number no reader compares against anything. Name them.
	if len(a.Untested) > 0 {
		out += fmt.Sprintf(", %d with no test files", len(a.Untested))
	}
	out += "\n"
	// The budget line. The totals say what the suite did; this says what it
	// cost, from the same event stream, so every future conversation about
	// whether a gate is too slow starts from a printed number instead of a
	// guess. It prints on every run — a refusal needs its budget data as much
	// as a pass does.
	out += a.slowestLine()
	if !withPackages {
		return out
	}
	if len(a.Untested) > 0 {
		for _, name := range a.Untested {
			out += "  no test files\t" + name + "\n"
		}
	}
	if a.Skipped == 0 {
		return out
	}
	return out + a.skipBreakdown()
}

// slowestLine names the three packages that took the longest, worst first, in
// the shape `slowest web 160s modkit 136s jobs 37s`. The package's last path
// element is enough to recognise it at a glance; the per-package lines above
// already carry the full import path for anyone who needs it.
func (a goTestAccount) slowestLine() string {
	if len(a.Packages) == 0 {
		return ""
	}
	slowest := append([]*packageAccount(nil), a.Packages...)
	sort.SliceStable(slowest, func(i, j int) bool {
		if slowest[i].Elapsed != slowest[j].Elapsed {
			return slowest[i].Elapsed > slowest[j].Elapsed
		}
		return slowest[i].Name < slowest[j].Name
	})
	if len(slowest) > 3 {
		slowest = slowest[:3]
	}
	parts := make([]string, 0, len(slowest))
	for _, pkg := range slowest {
		parts = append(parts, fmt.Sprintf("%s %.0fs", shortPackage(pkg.Name), pkg.Elapsed))
	}
	return "slowest " + strings.Join(parts, " ") + "\n"
}

// shortPackage is the recognisable tail of an import path:
// github.com/gogogadget/gogogadget/internal/web -> web.
func shortPackage(name string) string {
	if at := strings.LastIndex(name, "/"); at >= 0 {
		return name[at+1:]
	}
	return name
}

// unreasonedMarkerRefusal names every test that exempted itself from the skip
// refusal without saying what it was exempting itself for.
//
// The marker is the one self-service escape in this gate: any test may write
// it and none is refused for it. What makes that safe is the reason — the
// gate cannot tell an absent service from an inapplicable case by looking, so
// only the test knows, and a marker with nothing after it says nothing.
func (a goTestAccount) unreasonedMarkerRefusal() string {
	out := fmt.Sprintf("%d test(s) declared themselves %s and gave no reason:\n", len(a.Unreasoned), InapplicableSkipMarker)
	for _, name := range a.Unreasoned {
		out += "  " + name + "\n"
	}
	return out + "The marker exempts a skip from the CI refusal, so it has to carry the claim it is making: put the reason after it in the skip message."
}

// refuseEmptyRun is the floor. A suite that reported no package and no test
// is not a suite that passed, and `go test ./...` against a tree matching no
// packages exits 0 with nothing but a warning — so without this the gate
// prints a clean account of nothing and returns success, which is the exact
// failure it was built to close.
func (a goTestAccount) refuseEmptyRun() error {
	if len(a.Packages) == 0 {
		return refusalError(fmt.Errorf("the suite reported no packages at all: `go test ./...` matched nothing, so this run proves nothing. Generate the project's Go packages first (`ggg generate`), or name a tree that has some"))
	}
	if a.Passed+a.Skipped+a.Inapplicable+a.Failed == 0 {
		return refusalError(fmt.Errorf("the suite reported %d package(s) and not one test: a run that executed nothing is not a run that passed", len(a.Packages)))
	}
	return nil
}

func (a goTestAccount) forbiddenSkipRefusal() string {
	return fmt.Sprintf("%d test(s) skipped where every service the suite needs is provided (CI is set): a skipped test is not a passing test\n",
		a.Skipped) + a.skipBreakdown() +
		"Supply what those tests skip for, make the skip a failure at its source, or — if the case genuinely does not apply here — declare it by putting " +
		InapplicableSkipMarker + " in the skip message, which is counted separately and never refused."
}

// skipBreakdown is one line per skipping package, worst first.
func (a goTestAccount) skipBreakdown() string {
	out := ""
	for _, pkg := range a.skippingPackages() {
		out += fmt.Sprintf("  skipped %d of %d\t%s\n", pkg.Skipped,
			pkg.Passed+pkg.Failed+pkg.Skipped+pkg.Inapplicable, pkg.Name)
	}
	return out
}

// skippingPackages orders by skip count, so the package that hid the most sits
// at the top of a refusal.
func (a goTestAccount) skippingPackages() []*packageAccount {
	out := make([]*packageAccount, 0, len(a.Packages))
	for _, pkg := range a.Packages {
		if pkg.Skipped > 0 {
			out = append(out, pkg)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Skipped != out[j].Skipped {
			return out[i].Skipped > out[j].Skipped
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func coverageOf(output string) string {
	index := strings.Index(output, "coverage: ")
	if index < 0 {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(output[index:]), "\n")
}

func writeString(w io.Writer, s string) {
	if w == nil || s == "" {
		return
	}
	_, _ = io.WriteString(w, s)
}

// generatedOutputDigests is the content of every generated file in the tree,
// keyed by project-relative slash path.
//
// The set is modkit's own generated-output class — the `*_registry_gen.*`
// aggregates this engine renders plus the templ, sqlc and Tailwind outputs it
// declines to own — so a new emitter or a new generator is covered by the
// definition that already authorises the stale sweep to delete these paths,
// rather than by a second list kept in step by hand.
func generatedOutputDigests(root string) (map[string]string, error) {
	digests := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		slashed := filepath.ToSlash(relative)
		if entry.IsDir() {
			if slashed != "." && skipsGenerationScan(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || !modkit.IsGeneratedOutputPath(slashed) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		digests[slashed] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, err
	}
	return digests, nil
}

// skipsGenerationScan names the directories no generator writes into. bin/,
// tmp/ and .ggg/ are ignored build and state trees, node_modules is a
// dependency tree with its own generated files that this project neither
// renders nor owns, and .git is not source at all.
func skipsGenerationScan(name string) bool {
	switch name {
	case ".git", ".ggg", ".worktrees", "bin", "tmp", "node_modules", "playwright-report", "test-results":
		return true
	}
	return false
}

// refuseGenerationDrift compares the generated tree from before a generation
// run with the tree after it, and refuses when the run changed anything.
//
// This is the mechanism, and running the real generators is why it is sound.
// Every cheaper test was rejected: an mtime comparison fails on a fresh
// checkout, where every file is written in one pass and the order is
// arbitrary; a digest of the source recorded beside the output only moves the
// question to whether the record was written by the generator or by the
// command being verified; and nothing inside a `_templ.go` names the bytes it
// was generated from. Generation here is deterministic — same sources, same
// pinned tool versions, same output — so a run that changes a file is a
// statement that the file did not match its source.
//
// It refuses rather than accepting the repair, which is the property the
// brief asks for: `check` verifies, and a `check` that silently rewrites a
// stale artifact and then reports success is reporting on a tree that did not
// exist when it started.
func refuseGenerationDrift(before, after map[string]string) error {
	stale := make([]string, 0, 8)
	for path, digest := range after {
		if previous, existed := before[path]; !existed || previous != digest {
			stale = append(stale, path)
		}
	}
	for path := range before {
		if _, survived := after[path]; !survived {
			stale = append(stale, path)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	message := fmt.Sprintf("generated output was stale: %d file(s) did not match their declared source until this command regenerated them\n", len(stale))
	for _, path := range stale {
		message += "  " + path + generatedFrom(path) + "\n"
	}
	return refusalError(fmt.Errorf("%sThose files have been rewritten. Commit them, then re-run: a command that reports success must not do so over generated output its source no longer produces", message))
}

// generatedFrom names the declared source of a generated path when the path
// itself determines it. templ's output is the only one-to-one case; sqlc,
// Tailwind and the registry aggregates each derive from a whole set, and
// naming one member of it would be a guess.
func generatedFrom(path string) string {
	if stem, ok := strings.CutSuffix(path, "_templ.go"); ok {
		return " (from " + stem + ".templ)"
	}
	return ""
}

// ------------------------------------------------- the genesis-sweep trigger ----

// genesisSweepEnv is both halves of the sweep's opt-in: CI's `profiles` job
// sets it to un-skip the four-profile genesis sweep inside a raw `go test`,
// and `ggg check` treats it as the operator's statement that the sweep is
// this run's concern — the refusal below does not fire, and because the env
// reaches the accounted suite, the sweep runs inside `ggg check` itself.
const genesisSweepEnv = "GGG_GENESIS_SWEEP"

// genesisSweepTest is the sweep's `-run` value. The genesis trigger and the
// CI `profiles` job both name it; ci_workflow_test.go pins the CI half.
const genesisSweepTest = "TestEveryShippedProfileCreatesAProjectThatIsSyncClean"

// genesisSweepRemedy is the exact command a refusal names. It is one line an
// operator can paste, carrying the env and the -count=1 that a cached verdict
// would hollow out.
const genesisSweepRemedy = genesisSweepEnv + "=1 go test ./internal/gggcli -run " + genesisSweepTest + " -count=1"

// shippedPathSet is what a working-tree diff is compared against: every path
// whose bytes reach a derivative.
type shippedPathSet struct {
	// payloads maps each non-self_host payload an installed module declares
	// to the module that ships it. A self_host payload asserts about THIS
	// repository and never installs anywhere, so it is not in the set.
	payloads map[string]string
	// declarations maps every module manifest and profile declaration the
	// catalog publishes to the id it belongs to. The sweep walks
	// catalog.Profiles and the catalog loader parses every manifest, so a
	// declaration edit can break genesis without shipping a single payload
	// byte — profiles in particular are never locked, and three of the four
	// shipped ones are not installed here.
	declarations map[string]string
}

// shippedPathsForSweep derives the shipped-path set from the lock and the
// catalog, never from a hand-kept list. The second return is a skip reason:
// the environments the trigger cannot run in (no lock, no catalog — every
// derivative) are named and passed, not silently passed.
func shippedPathsForSweep(root string) (set shippedPathSet, skipped string, err error) {
	lock, hasLock, lockErr := readProjectLock(root)
	if lockErr != nil {
		return shippedPathSet{}, "the lock would not parse (" + errOneLine(lockErr) + "); the sync step will name the cause", nil
	}
	if !hasLock || len(lock.Modules) == 0 {
		return shippedPathSet{}, "no lock recording installed modules, so nothing ships from this tree (a fresh genesis starts here; `ggg setup` is the next command)", nil
	}
	set = shippedPathSet{payloads: map[string]string{}, declarations: map[string]string{}}
	for _, locked := range lock.Modules {
		for _, file := range locked.Manifest.Files {
			if file.SelfHost || file.Source == "" {
				continue
			}
			set.payloads[file.Source] = locked.Manifest.ID
		}
	}
	catalog, catalogErr := modkit.LoadCatalog(os.DirFS(root))
	if catalogErr != nil {
		// A derivative resolves a remote registry and has no catalog tree at
		// its root at all, so this is the normal derivative path, not a
		// failure: the sweep's own fixtures skip [inapplicable] there too.
		return shippedPathSet{}, "the catalog would not load (" + errOneLine(catalogErr) + "); a tree that publishes no registry has nothing shipped to sweep", nil
	}
	for _, module := range catalog.Modules {
		set.declarations[moduleManifestPath(module)] = module.ID
	}
	for _, profile := range catalog.Profiles {
		set.declarations["registry/profiles/"+profile.Name+".json"] = profile.ID
	}
	// The floor, and it is this file's own thesis again: a derivation that
	// silently produced an empty or partial set would make the trigger
	// vacuously green over every payload diff — the exact shape of failure
	// this gate exists to close. Every module contributes exactly one
	// declaration path and at least one shipped payload (measured today:
	// 293 locked modules -> 1,297 payload paths and 297 declarations), so
	// counts below the module count mean the derivation collapsed, not that
	// the registry shrank.
	if len(set.declarations) < len(lock.Modules) {
		return shippedPathSet{}, "", collapsedShippedPaths(len(lock.Modules), len(set.payloads), len(set.declarations))
	}
	if len(set.payloads) < len(lock.Modules) {
		return shippedPathSet{}, "", collapsedShippedPaths(len(lock.Modules), len(set.payloads), len(set.declarations))
	}
	return set, "", nil
}

func collapsedShippedPaths(modules, payloads, declarations int) error {
	return refusalError(fmt.Errorf(
		"the shipped-path derivation collapsed: %d locked modules produced %d payload path(s) and %d declaration path(s). "+
			"A trigger diffing against an empty or partial set is vacuously green over every payload diff — "+
			"fix the derivation before trusting any `ggg check` that ran through it", modules, payloads, declarations))
}

// moduleManifestPath is where a catalog module's declaration lives, per the
// registry's own layout. Profiles are not a module kind — they publish
// outside registry/modules/ as registry/profiles/<name>.json and are added
// beside this from catalog.Profiles.
func moduleManifestPath(module modkit.Manifest) string {
	return "registry/modules/" + string(module.Kind) + "/" + module.Name + "/module.json"
}

// refuseUnsweptShippedPayloadDiff is `ggg check`'s third honesty rule. The
// v0.20.0 incident: a diff that touched shipped test payloads broke
// derivative compilation, and the only gate that would have caught it — the
// four-profile genesis sweep — was opt-in, so the first CI run of the release
// was the first time anyone created a project from the changed registry.
//
// It refuses rather than auto-running the sweep (the sweep needs the network
// and ~93 s; a refusal is cheaper, it teaches, and it names the files), and
// the refusal is lifted by the same env var that runs the sweep:
// `GGG_GENESIS_SWEEP=1 ggg check` executes the sweep inside the accounted
// suite instead of refusing.
func (c *Controller) refuseUnsweptShippedPayloadDiff(ctx context.Context, root string) error {
	progress := taskProgress(c.runner())
	if os.Getenv(genesisSweepEnv) == "1" {
		writeString(progress, "genesis sweep: "+genesisSweepEnv+"=1 is set, so the sweep runs inside this check's accounted suite instead of being refused\n")
		return nil
	}
	runner, ok := c.runner().(TaskOutputRunner)
	if !ok {
		writeString(progress, "genesis sweep trigger skipped: the task runner cannot read git output, so the diff would be a guess\n")
		return nil
	}
	set, skipped, err := shippedPathsForSweep(root)
	if err != nil {
		return err
	}
	if skipped != "" {
		writeString(progress, "genesis sweep trigger skipped: "+skipped+"\n")
		return nil
	}
	changed, skip := changedAgainstOriginMain(ctx, runner, root)
	if skip != "" {
		writeString(progress, skip+"\n")
		return nil
	}
	var hits []string
	for _, path := range changed {
		if module, shipped := set.payloads[path]; shipped {
			hits = append(hits, path+"  (payload of "+module+")")
		} else if owner, declared := set.declarations[path]; declared {
			hits = append(hits, path+"  (declaration of "+owner+")")
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.Strings(hits)
	message := fmt.Sprintf(
		"this diff changes %d path(s) whose bytes reach derivatives, and nothing has proved the shipped profiles still create a sync-clean project:\n",
		len(hits))
	for _, hit := range hits {
		message += "  " + hit + "\n"
	}
	message += "The genesis sweep is opt-in because it needs the network and ~93 s, which is how a derivative-compile break reached a green local gate in v0.20.0. Run it before pushing:\n" +
		"  " + genesisSweepRemedy + "\n" +
		"The refusal stands on every re-run of `ggg check` over this diff by design. It is lifted by " + genesisSweepEnv + "=1, which runs the sweep inside the accounted suite instead of refusing."
	return refusalError(fmt.Errorf("%s", message))
}

// changedAgainstOriginMain lists the paths the working tree changed since the
// merge-base with origin/main. The second return is a skip reason: no origin
// (fresh clone, closed tree), no git at all (a derivative that never init'ed
// one), or a diff that could not be read all degrade to a stated skip rather
// than a guess — the same rule the accounted suite applies to a runner that
// cannot read `go test -json`.
func changedAgainstOriginMain(ctx context.Context, runner TaskOutputRunner, root string) ([]string, string) {
	var base strings.Builder
	if err := runner.RunOutput(ctx, root, []string{"git", "merge-base", "HEAD", "origin/main"}, nil, &base); err != nil {
		return nil, "genesis sweep trigger skipped: no origin/main merge-base to diff against — a fresh clone, a closed tree, or no git (" + errOneLine(err) + ")"
	}
	var diff strings.Builder
	if err := runner.RunOutput(ctx, root, []string{"git", "diff", "--name-only", strings.TrimSpace(base.String())}, nil, &diff); err != nil {
		return nil, "genesis sweep trigger skipped: the diff against the merge-base could not be read (" + errOneLine(err) + ")"
	}
	var paths []string
	for line := range strings.SplitSeq(diff.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths, ""
}

// taskProgress is where a gate's own notes belong: the same stream the
// accounted summary prints to, so one run reads as one voice.
func taskProgress(runner TaskRunner) io.Writer {
	capable, ok := runner.(TaskOutputRunner)
	if !ok {
		return nil
	}
	return capable.Progress()
}

// errOneLine flattens an error whose text carries a child's indented output
// tail into a single line, for skip notes that must not bury their own reason.
func errOneLine(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}
