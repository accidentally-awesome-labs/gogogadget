// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ciWorkflowPath is this repository's own gate. It is a module payload owned by
// ggg/system/ci-github, which is why it is asserted here rather than trusted:
// nobody in this repository executes it, so the only thing that can catch a
// job being deleted, gated off, or reduced to something that always passes is a
// test that reads it.
const ciWorkflowPath = ".github/workflows/ci.yml"

// ciRegistryValidateJobs is the required split: one CI job per selectable
// closure family. `ggg registry validate` is the only gate that proves a
// module can be installed, compiled, tested, removed and restored byte for
// byte, and the families are separate claims — this repository's fixtures
// versus a third party's signed registry — so they are separate jobs with
// separate work directories.
var ciRegistryValidateJobs = map[string]ClosureFamily{
	"registry-core":     ClosureFamilyCore,
	"registry-external": ClosureFamilyExternal,
}

// ciValidateCommandPrefixes are the two legitimate ways to invoke the CLI in
// CI: the binary `make setup` just built, or the source it was built from.
var ciValidateCommandPrefixes = [][]string{
	{"bin/ggg"},
	{"go", "run", "./cmd/ggg"},
}

// Every selectable closure family must be exercised by a CI job that can
// actually fail, and each family must have something to exercise.
//
// The two halves matter for different reasons. Reading the workflow catches the
// job disappearing, being pinned behind an `if:`, being marked
// continue-on-error, or having its command wrapped in something that discards
// the exit status — the shapes that turn a gate into decoration. Resolving the
// families through the same function the command uses catches the opposite
// failure: a job that runs correctly and proves nothing, because the fixtures
// it names are gone.
func TestCIExercisesEveryClosureFamilyForReal(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	workflow, goVersion := readCIWorkflow(t, root)

	for _, family := range selectableClosureFamilies {
		jobName, ok := ciJobForFamily(family)
		if !ok {
			t.Fatalf("closure family %q has no CI job; add one to %s and to ciRegistryValidateJobs",
				family, ciWorkflowPath)
		}
		job, ok := workflow.Jobs[jobName]
		if !ok {
			t.Fatalf("%s has no %s job, so the %s closures are never validated in CI",
				ciWorkflowPath, jobName, family)
		}
		assertCIJobIsARealGate(t, jobName, job, goVersion)
		assertCIJobValidates(t, jobName, job, family)

		// The command in the workflow is only a gate if the family it names
		// resolves to work in this repository. Cheap: collecting closures
		// reads manifests, it does not build anything.
		closures, closureErr := closuresForFamily(root, family, io.Discard)
		if closureErr != nil {
			t.Fatalf("%s job would fail before exercising anything: %v", jobName, closureErr)
		}
		if len(closures) == 0 {
			t.Fatalf("%s job exercises no closures, so it is an always-green no-op", jobName)
		}
		t.Logf("%s exercises %d closure(s) of family %s", jobName, len(closures), family)
	}

	// `all` is the union, never a job: two jobs both running every closure
	// would double the work and give up the isolation the split is for.
	for name, family := range ciRegistryValidateJobs {
		if family == ClosureFamilyAll {
			t.Fatalf("job %s claims the all family; the CI split is one job per selectable family", name)
		}
	}
}

// ciSuiteCommandPrefixes are the two legitimate ways to invoke the accounted
// suite in CI: the binary `make setup` just built, or the source it was built
// from. Mirrors ciValidateCommandPrefixes, and for the same reason — the
// invocation must be recognised structurally, not by substring.
var ciSuiteCommandPrefixes = [][]string{
	{"bin/ggg", "test"},
	{"go", "run", "./cmd/ggg", "test"},
}

// ciSuiteModes are the `ggg test` layers that run Go packages. The mode is
// pinned because `ggg test smoke` satisfies every other assertion here and
// runs no Go test at all. `unit` was a third accepted mode until it turned
// out to be `integration` under another name.
var ciSuiteModes = []string{"integration", "all"}

// The `test` job's suite step must go through the CLI, because a bare
// `go test` cannot report what it did. `go test` never summarises skips: a
// package whose every fixture skipped prints the same `ok` as one that ran,
// and that is how an entire integration layer skipped against a torn-down
// stack under four green `ok` lines. CI is the one place a skip cannot be
// legitimate — this job's own env block names TEST_DATABASE_URL and its
// service container answers on it — and `ggg test` is what reads the event
// stream and refuses a nonzero skip count.
//
// The command is matched as a COMMAND, not by substring, which is the
// standard isMakeSetupStep sets in this file: `run: echo "ggg test
// integration --race --cover"` satisfies a substring match while running no
// suite, and a gate defeated by an echo is the exact shape this whole change
// exists to close. The race detector and the coverage flag are both pinned,
// or closing one hole opens a worse one, and the mode is pinned because
// `ggg test smoke` would otherwise pass.
//
// Mutation: put `go test -race -cover ./...` back, wrap the command in an
// echo, drop --cover, or change the mode to smoke, and this fails naming the
// command it found.
func TestCITestJobRunsTheAccountedSuiteUnderRace(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	workflow, _ := readCIWorkflow(t, root)
	job, ok := workflow.Jobs["test"]
	if !ok {
		t.Fatal("the workflow has no test job")
	}

	var found []string
	var accounted [][]string
	setupIndex, suiteIndex := -1, -1
	for index, step := range job.Steps {
		if isMakeSetupStep(step) {
			setupIndex = index
		}
		for line := range strings.SplitSeq(step.Run, "\n") {
			command := strings.TrimSpace(line)
			fields := strings.Fields(command)
			if len(fields) == 0 {
				continue
			}
			// A bare `go test` is detected by its own first word, so it
			// cannot slip back in unnoticed; anything else has to parse as
			// one of the declared CLI invocations.
			args, isAccounted := parseCISuiteCommand(fields)
			if !isAccounted && !(fields[0] == "go" && len(fields) > 1 && fields[1] == "test") {
				continue
			}
			if step.If != "" || step.ContinueOnError {
				t.Fatalf("the suite step is exempt from failing the build (if: %q, continue-on-error: %v)", step.If, step.ContinueOnError)
			}
			if strings.ContainsAny(strings.TrimSpace(step.Run), "\n|;&>") || strings.Contains(step.Run, "set +e") {
				t.Fatalf("the suite step wraps the command in shell that can hide its exit status: %q", step.Run)
			}
			found = append(found, command)
			suiteIndex = index
			if isAccounted {
				accounted = append(accounted, args)
			}
		}
	}

	if len(found) != 1 {
		t.Fatalf("the test job runs the suite %d times: %q; want exactly one accounted run", len(found), found)
	}
	if len(accounted) != 1 {
		t.Fatalf("the test job runs %q, which is not an accounted invocation: a bare `go test` cannot report a skip and so cannot refuse one", found[0])
	}
	if setupIndex < 0 || setupIndex > suiteIndex {
		t.Fatalf("the test job runs the suite before `make setup`, so bin/ggg does not exist yet")
	}
	args := accounted[0]
	if len(args) == 0 || !slices.Contains(ciSuiteModes, args[0]) {
		t.Fatalf("the suite command %q names mode %q, want one of %v: the other layers run no Go test", found[0], args, ciSuiteModes)
	}
	for _, want := range []string{"--race", "--cover"} {
		if !slices.Contains(args, want) {
			t.Fatalf("the suite command %q dropped %s", found[0], want)
		}
	}
}

// ciGenesisSweepTest is the one test the `profiles` job exists to run, and
// the reason that job needs asserting at all: the sweep skips itself as
// [inapplicable] unless GGG_GENESIS_SWEEP is set, and this job is the only
// place that sets it. Delete the job and the only end-to-end profile gate
// goes green-by-skip in every environment without one test turning red.
//
// That is not hypothetical. Until the matrix existed, `saas` was the only
// profile ever created end to end, and three profiles that could not produce
// a project at all shipped for a release.
const ciGenesisSweepTest = "TestEveryShippedProfileCreatesAProjectThatIsSyncClean"

// The `profiles` job must run the genesis sweep, with the environment
// variable that un-skips it, as a command that can fail the build.
//
// Mutation: drop the env block, wrap the command in an echo, add an `if:`,
// point `-run` at another test, drop `-count=1`, or delete the job, and this
// fails naming what it found.
func TestCIProfilesJobRunsTheGenesisSweep(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	workflow, goVersion := readCIWorkflow(t, root)
	job, ok := workflow.Jobs["profiles"]
	if !ok {
		t.Fatalf("%s has no profiles job, so nothing sets GGG_GENESIS_SWEEP and %s never runs anywhere",
			ciWorkflowPath, ciGenesisSweepTest)
	}
	assertCIJobIsARealGate(t, "profiles", job, goVersion)
	if got := job.Env["GGG_GENESIS_SWEEP"]; got != "1" {
		t.Fatalf("the profiles job sets GGG_GENESIS_SWEEP=%q, want \"1\"; without it %s skips itself",
			got, ciGenesisSweepTest)
	}

	var found []string
	setupIndex, sweepIndex := -1, -1
	for index, step := range job.Steps {
		if isMakeSetupStep(step) {
			setupIndex = index
		}
		for line := range strings.SplitSeq(step.Run, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			// Matched as a COMMAND by its own first words, the standard the
			// rest of this file sets: `echo "go test -run ..."` runs no test
			// and must not satisfy the assertion.
			if len(fields) < 2 || fields[0] != "go" || fields[1] != "test" {
				continue
			}
			if step.If != "" || step.ContinueOnError {
				t.Fatalf("the sweep step is exempt from failing the build (if: %q, continue-on-error: %v)",
					step.If, step.ContinueOnError)
			}
			if strings.ContainsAny(strings.TrimSpace(step.Run), "\n|;&>") || strings.Contains(step.Run, "set +e") {
				t.Fatalf("the sweep step wraps the command in shell that can hide its exit status: %q", step.Run)
			}
			found = append(found, strings.Join(fields, " "))
			sweepIndex = index
		}
	}
	if len(found) != 1 {
		t.Fatalf("the profiles job runs `go test` %d time(s): %q; want exactly one sweep", len(found), found)
	}
	fields := strings.Fields(found[0])
	at := slices.Index(fields, "-run")
	if at < 0 || at+1 >= len(fields) {
		t.Fatalf("the sweep command %q carries no -run filter, so it runs whatever ./internal/gggcli happens to contain", found[0])
	}
	if fields[at+1] != ciGenesisSweepTest {
		t.Fatalf("the sweep command %q does not select %s", found[0], ciGenesisSweepTest)
	}
	// The pinned name is a STRING until something reads the package it names.
	// `go test -run` treats a pattern that matches nothing as success — it
	// prints `no tests to run` and exits 0 — so a rename of the sweep would
	// leave this guard green over a CI job that runs no test at all. Both
	// halves are asserted against the declarations on disk: the exact
	// function must exist, and the pattern as written must select something.
	assertRunFilterSelectsAGGGCLITest(t, fields[at+1])
	// -count=1 for the reason the accounted suite pins it: this test's inputs
	// are the whole registry tree, which `go test` cannot observe, so a
	// cached verdict is a report on a run that did not happen.
	for _, want := range []string{"-count=1", "./internal/gggcli"} {
		if !slices.Contains(fields, want) {
			t.Fatalf("the sweep command %q is missing %s", found[0], want)
		}
	}
	if setupIndex < 0 || setupIndex > sweepIndex {
		t.Fatal("the profiles job runs the sweep before `make setup`, so the pinned tools are absent")
	}
}

// gggcliTestNames is every top-level `func TestXxx(t *testing.T)` declared in
// internal/gggcli, read out of the package's own sources. It is the
// population `go test -run` filters, derived rather than written down.
func gggcliTestNames(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("..", "gggcli")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	declaration := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, match := range declaration.FindAllStringSubmatch(string(raw), -1) {
			names = append(names, match[1])
		}
	}
	slices.Sort(names)
	// The floor. A walk that found the wrong directory, or a regexp that
	// stopped matching, would otherwise answer "nothing matches" for every
	// pattern and turn this guard into the vacuity it exists to refuse.
	if len(names) < 50 {
		t.Fatalf("only %d top-level tests were read out of %s; the walk has collapsed, not the package", len(names), dir)
	}
	return names
}

// assertRunFilterSelectsAGGGCLITest refuses a `-run` pattern that selects no
// test. This is the vacuity `go test` itself will not report: an unmatched
// filter is a warning on stdout and exit 0, so a CI job pinned to a renamed
// test passes while running nothing.
func assertRunFilterSelectsAGGGCLITest(t *testing.T, pattern string) {
	t.Helper()
	names := gggcliTestNames(t)
	if !slices.Contains(names, pattern) {
		// Reported first and by name, because an exact pin is what the
		// workflow carries and a near-miss rename is the realistic failure.
		t.Errorf("the workflow pins -run %s and internal/gggcli declares no `func %s(`.\n"+
			"`go test -run %s ./internal/gggcli` exits 0 with `no tests to run`, so the job is green and the gate is gone. "+
			"Repoint the workflow and %s at the test's current name, or restore the name.",
			pattern, pattern, pattern, "ciGenesisSweepTest")
	}
	// `go test -run` compiles its argument as an unanchored regexp over
	// top-level names, so the pin is checked the way the toolchain reads it
	// as well as literally: a pattern that is nobody's name selects nothing.
	filter, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("the workflow's -run %q is not a valid regexp, so `go test` refuses it: %v", pattern, err)
	}
	if !slices.ContainsFunc(names, filter.MatchString) {
		t.Fatalf("the workflow's -run %q matches none of the %d tests internal/gggcli declares; the job runs nothing and exits 0",
			pattern, len(names))
	}
}

// parseCISuiteCommand returns the arguments after a recognised `ggg test`
// invocation. The command's own first words must BE the invocation, so an
// `echo` or any other wrapper fails to parse rather than matching.
func parseCISuiteCommand(fields []string) ([]string, bool) {
	for _, prefix := range ciSuiteCommandPrefixes {
		if len(fields) >= len(prefix) && slices.Equal(fields[:len(prefix)], prefix) {
			return fields[len(prefix):], true
		}
	}
	return nil, false
}

func ciJobForFamily(family ClosureFamily) (string, bool) {
	for name, candidate := range ciRegistryValidateJobs {
		if candidate == family {
			return name, true
		}
	}
	return "", false
}

// assertCIJobIsARealGate checks the wiring every downstream job in this
// workflow shares, and that nothing exempts the job from failing the build.
func assertCIJobIsARealGate(t *testing.T, name string, job ciJob, goVersion string) {
	t.Helper()
	if job.If != "" {
		t.Fatalf("job %s is gated by if: %q, so it can be skipped silently", name, job.If)
	}
	if job.ContinueOnError {
		t.Fatalf("job %s is continue-on-error, so a failed closure would not fail the build", name)
	}
	if !slices.Contains(job.Needs, "test") {
		t.Fatalf("job %s does not need the test job; every downstream job in %s does", name, ciWorkflowPath)
	}
	if job.RunsOn == "" {
		t.Fatalf("job %s declares no runner", name)
	}

	var setupGo ciStep
	var hasCheckout, hasSetup bool
	for _, step := range job.Steps {
		switch {
		case step.Uses == "actions/checkout@v7":
			hasCheckout = true
		case strings.HasPrefix(step.Uses, "actions/setup-go@"):
			setupGo = step
		case isMakeSetupStep(step):
			hasSetup = true
		}
	}
	if !hasCheckout {
		t.Fatalf("job %s does not check out the repository at actions/checkout@v7", name)
	}
	if setupGo.Uses != "actions/setup-go@v7" {
		t.Fatalf("job %s uses %q; every job in %s pins actions/setup-go@v7", name, setupGo.Uses, ciWorkflowPath)
	}
	if got := setupGo.With["go-version"]; got != goVersion {
		t.Fatalf("job %s pins Go %q, the test job pins %q", name, got, goVersion)
	}
	if got := setupGo.With["cache"]; got != "true" {
		t.Fatalf("job %s does not enable the Go build cache", name)
	}
	if !hasSetup {
		t.Fatalf("job %s never runs make setup, so bin/ggg and the pinned tools are absent", name)
	}
}

// isMakeSetupStep reports whether the step runs `make setup` as a command
// rather than merely mentioning it. A substring match accepts
// `echo "make setup"`, which installs nothing and builds no bin/ggg, so the
// check reads the step's command lines instead.
func isMakeSetupStep(step ciStep) bool {
	for line := range strings.SplitSeq(step.Run, "\n") {
		if strings.TrimSpace(line) == "make setup" {
			return true
		}
	}
	return false
}

// assertCIJobValidates finds the one validate invocation in the job and checks
// it is a bare command for the expected family. Bare is the point: a pipe, a
// `|| true`, a `set +e` or an `if:` on the step would all leave a green job
// over a failed closure.
func assertCIJobValidates(t *testing.T, name string, job ciJob, family ClosureFamily) {
	t.Helper()
	var invocations []ciStep
	setupIndex, validateIndex := -1, -1
	for index, step := range job.Steps {
		if isMakeSetupStep(step) {
			setupIndex = index
		}
		if strings.Contains(step.Run, "registry validate") {
			invocations = append(invocations, step)
			validateIndex = index
		}
	}
	if len(invocations) != 1 {
		t.Fatalf("job %s runs `registry validate` %d times; want exactly one invocation", name, len(invocations))
	}
	step := invocations[0]
	if step.If != "" {
		t.Fatalf("job %s gates its validate step by if: %q", name, step.If)
	}
	if step.ContinueOnError {
		t.Fatalf("job %s marks its validate step continue-on-error", name)
	}
	if setupIndex > validateIndex {
		t.Fatalf("job %s runs make setup after validating, so bin/ggg does not exist yet", name)
	}

	command := strings.TrimSpace(step.Run)
	if strings.ContainsAny(command, "\n|;&>") || strings.Contains(command, "set +e") {
		t.Fatalf("job %s wraps the validator in shell that can hide its exit status: %q", name, command)
	}
	got, err := parseCIValidateFamily(command)
	if err != nil {
		t.Fatalf("job %s: %v", name, err)
	}
	if got != family {
		t.Fatalf("job %s validates the %s family, want %s", name, got, family)
	}
}

// parseCIValidateFamily reads the family out of a validate command line,
// through the CLI's own parser, so the workflow and the engine cannot drift
// into two different vocabularies.
func parseCIValidateFamily(command string) (ClosureFamily, error) {
	fields := strings.Fields(command)
	for _, prefix := range ciValidateCommandPrefixes {
		if len(fields) >= len(prefix) && slices.Equal(fields[:len(prefix)], prefix) {
			fields = fields[len(prefix):]
			break
		}
	}
	if len(fields) < 2 || fields[0] != "registry" || fields[1] != "validate" {
		return "", fmt.Errorf("not a `ggg registry validate` invocation: %q", command)
	}
	args := fields[2:]
	for index, field := range args {
		value := ""
		switch {
		case field == "--closures":
			if index+1 >= len(args) {
				return "", fmt.Errorf("--closures has no value in %q", command)
			}
			value = args[index+1]
		case strings.HasPrefix(field, "--closures="):
			value = strings.TrimPrefix(field, "--closures=")
		default:
			continue
		}
		family, err := ParseClosureFamily(value)
		if err != nil {
			return "", err
		}
		if family == ClosureFamilyAll {
			return "", fmt.Errorf("--closures %s is the union of every family, not one of them: %q", value, command)
		}
		return family, nil
	}
	return "", fmt.Errorf("no --closures selection in %q, so the job is not family-scoped", command)
}

// readCIWorkflow parses the workflow, checks it still runs on every change,
// and returns the toolchain the test job pins, so a Go bump stays a one-place
// edit in the workflow itself.
func readCIWorkflow(t *testing.T, root string) (ciWorkflow, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ciWorkflowPath)))
	if err != nil {
		t.Fatalf("read %s: %v", ciWorkflowPath, err)
	}
	var workflow ciWorkflow
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse %s: %v", ciWorkflowPath, err)
	}
	assertCIRunsOnEveryChange(t, workflow.On)
	base, ok := workflow.Jobs["test"]
	if !ok {
		t.Fatalf("%s has no test job to take the pinned toolchain from", ciWorkflowPath)
	}
	goVersion := ""
	for _, step := range base.Steps {
		if strings.HasPrefix(step.Uses, "actions/setup-go@") {
			goVersion = step.With["go-version"]
		}
	}
	if goVersion == "" {
		t.Fatalf("%s test job pins no Go version", ciWorkflowPath)
	}
	return workflow, goVersion
}

// assertCIRunsOnEveryChange checks the triggers. Every job in this file is
// decoration if the workflow only runs on demand: narrowing `on:` to
// workflow_dispatch leaves each job perfectly well-formed and never runs one.
// Pull requests are where a change is reviewed and pushes to main are where it
// lands, so both must fire.
func assertCIRunsOnEveryChange(t *testing.T, on yaml.Node) {
	t.Helper()
	if on.Kind != yaml.MappingNode {
		t.Fatalf("%s declares no trigger mapping, so nothing states when it runs", ciWorkflowPath)
	}
	triggers := map[string]*yaml.Node{}
	names := make([]string, 0, len(on.Content)/2)
	for index := 0; index+1 < len(on.Content); index += 2 {
		triggers[on.Content[index].Value] = on.Content[index+1]
		names = append(names, on.Content[index].Value)
	}
	push, ok := triggers["push"]
	if !ok {
		t.Fatalf("%s does not run on push; triggers are %v", ciWorkflowPath, names)
	}
	var pushTrigger struct {
		Branches []string `yaml:"branches"`
	}
	if err := push.Decode(&pushTrigger); err != nil {
		t.Fatalf("parse the push trigger of %s: %v", ciWorkflowPath, err)
	}
	if !slices.Contains(pushTrigger.Branches, "main") {
		t.Fatalf("%s does not run on pushes to main; branches are %v", ciWorkflowPath, pushTrigger.Branches)
	}
	if _, ok := triggers["pull_request"]; !ok {
		t.Fatalf("%s does not run on pull requests, so no change is gated before it lands; triggers are %v",
			ciWorkflowPath, names)
	}
}

type ciWorkflow struct {
	// On is read as a node because `pull_request:` legitimately carries no
	// value, and a nil-able struct cannot tell an absent key from a null one.
	On   yaml.Node        `yaml:"on"`
	Jobs map[string]ciJob `yaml:"jobs"`
}

type ciJob struct {
	Needs           ciStringList      `yaml:"needs"`
	RunsOn          string            `yaml:"runs-on"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	Steps           []ciStep          `yaml:"steps"`
}

type ciStep struct {
	Uses            string            `yaml:"uses"`
	With            map[string]string `yaml:"with"`
	Run             string            `yaml:"run"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
}

// ciStringList accepts both shapes GitHub allows for `needs`: one job name or
// a list of them.
type ciStringList []string

func (l *ciStringList) UnmarshalYAML(node *yaml.Node) error {
	var single string
	if err := node.Decode(&single); err == nil {
		*l = ciStringList{single}
		return nil
	}
	var many []string
	if err := node.Decode(&many); err != nil {
		return err
	}
	*l = many
	return nil
}
