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

// goTestAccount is what a suite run actually did, per package and in total.
type goTestAccount struct {
	Packages []*packageAccount
	Passed   int
	Skipped  int
	Failed   int
}

type packageAccount struct {
	Name     string
	Result   string
	Elapsed  float64
	Coverage string
	Passed   int
	Skipped  int
	Failed   int
	output   []string
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
			pkg.output = append(pkg.output, event.Output)
		case "pass", "fail", "skip":
			if event.Test != "" {
				switch event.Action {
				case "pass":
					pkg.Passed++
					account.Passed++
				case "skip":
					pkg.Skipped++
					account.Skipped++
				case "fail":
					pkg.Failed++
					account.Failed++
				}
				continue
			}
			pkg.Result = event.Action
			pkg.Elapsed = event.Elapsed
			writeString(progress, pkg.line())
			if event.Action == "fail" {
				// The failing package's own output IS the diagnosis, so it
				// goes through untouched.
				for _, out := range pkg.output {
					writeString(progress, out)
				}
			}
			pkg.output = nil
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
	switch {
	case p.Skipped > 0 && p.Passed == 0 && p.Failed == 0:
		// The incident, named where it happens: this line used to read `ok`
		// and nothing else.
		line += fmt.Sprintf("\tNO TEST RAN: all %d skipped", p.Skipped)
	case p.Skipped > 0:
		line += fmt.Sprintf("\t%d of %d skipped", p.Skipped, p.Passed+p.Failed+p.Skipped)
	}
	return line + "\n"
}

// summary is the count `go test` never prints. The totals line is emitted on
// every run, pass or fail, because a skip that is legitimate here is still a
// test that did not run and the operator has to be able to see it.
//
// withPackages is false when a refusal is about to print the same breakdown,
// so the list appears exactly once.
func (a goTestAccount) summary(withPackages bool) string {
	out := fmt.Sprintf("tests: %d passed, %d skipped, %d failed across %d packages\n",
		a.Passed, a.Skipped, a.Failed, len(a.Packages))
	if a.Skipped == 0 || !withPackages {
		return out
	}
	return out + a.skipBreakdown()
}

func (a goTestAccount) forbiddenSkipRefusal() string {
	return fmt.Sprintf("%d test(s) skipped where every service the suite needs is provided (CI is set): a skipped test is not a passing test\n",
		a.Skipped) + a.skipBreakdown() +
		"Supply what those tests skip for, or make the skip a failure at its source."
}

// skipBreakdown is one line per skipping package, worst first.
func (a goTestAccount) skipBreakdown() string {
	out := ""
	for _, pkg := range a.skippingPackages() {
		out += fmt.Sprintf("  skipped %d of %d\t%s\n", pkg.Skipped, pkg.Passed+pkg.Failed+pkg.Skipped, pkg.Name)
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
