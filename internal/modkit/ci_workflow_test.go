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
	"maps"
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

// assertCIJobRunsTheAccountedSuite checks everything a suite step must be:
// through the CLI (a bare `go test` cannot report what it did — `go test`
// never summarises skips, so a package whose every fixture skipped prints the
// same `ok` as one that ran, and that is how an entire integration layer
// skipped against a torn-down stack under four green `ok` lines), exactly
// once, after `make setup`, unexempted and unwrapped. CI is the one place a
// skip cannot be legitimate — the job's own env block names
// TEST_DATABASE_URL and its service container answers on it — and `ggg test`
// is what reads the event stream and refuses a nonzero skip count.
//
// The command is matched as a COMMAND, not by substring, the standard
// isMakeSetupStep sets in this file: `run: echo "ggg test integration
// --race"` satisfies a substring match while running no suite, and a gate
// defeated by an echo is the exact shape this whole change exists to close.
//
// want names flags the command must carry; refuse names flags it must not —
// the split is the budget, and either direction drifting back is a failure
// that names the command it found.
func assertCIJobRunsTheAccountedSuite(t *testing.T, workflow ciWorkflow, jobName string, want, refuse []string) {
	t.Helper()
	job, ok := workflow.Jobs[jobName]
	if !ok {
		t.Fatalf("the workflow has no %s job", jobName)
	}
	// The suite refuses skips in CI, so the job has to provide what the
	// database fixtures skip for, or it fails over services it never started.
	if job.Env["TEST_DATABASE_URL"] == "" {
		t.Fatalf("job %s names no TEST_DATABASE_URL, so every database fixture skips and the accounted suite refuses the run it was asked to gate", jobName)
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
				t.Fatalf("%s: the suite step is exempt from failing the build (if: %q, continue-on-error: %v)", jobName, step.If, step.ContinueOnError)
			}
			if strings.ContainsAny(strings.TrimSpace(step.Run), "\n|;&>") || strings.Contains(step.Run, "set +e") {
				t.Fatalf("%s: the suite step wraps the command in shell that can hide its exit status: %q", jobName, step.Run)
			}
			found = append(found, command)
			suiteIndex = index
			if isAccounted {
				accounted = append(accounted, args)
			}
		}
	}

	if len(found) != 1 {
		t.Fatalf("job %s runs the suite %d times: %q; want exactly one accounted run", jobName, len(found), found)
	}
	if len(accounted) != 1 {
		t.Fatalf("job %s runs %q, which is not an accounted invocation: a bare `go test` cannot report a skip and so cannot refuse one", jobName, found[0])
	}
	if setupIndex < 0 || setupIndex > suiteIndex {
		t.Fatalf("job %s runs the suite before `make setup`, so bin/ggg does not exist yet", jobName)
	}
	args := accounted[0]
	if len(args) == 0 || !slices.Contains(ciSuiteModes, args[0]) {
		t.Fatalf("%s: the suite command %q names mode %q, want one of %v: the other layers run no Go test", jobName, found[0], args, ciSuiteModes)
	}
	for _, flag := range want {
		if !slices.Contains(args, flag) {
			t.Fatalf("%s: the suite command %q dropped %s", jobName, found[0], flag)
		}
	}
	for _, flag := range refuse {
		if slices.Contains(args, flag) {
			t.Fatalf("%s: the suite command %q carries %s, which belongs to the other half of the split — re-merging the two instrumentations puts their multiplied cost back on one job's critical path", jobName, found[0], flag)
		}
	}
}

// The `test` job's suite runs under race. Race is the semantic gate — the
// detector that proves the concurrent paths actually work — so it owns the
// job named `test`, and coverage runs beside it in `cover` instead of after
// it: the single `--race --cover` step this replaced was 663 s of an 883 s
// job, and the green wall waited on both instrumentations for one job's
// worth of either signal.
//
// It is also the one `ci.yml` job whose checkout is pinned to fetch-depth: 0.
// The accounted suite carries a guard that measures every module's bytes
// against the registry snapshot of the last RELEASED tag, reading it out of
// git; on the default single-commit checkout that guard skips
// [inapplicable] on every push, which is a guard that is real on a
// contributor's machine and vacuous on the branch it gates. No other job
// runs it, so no other job pays the full history.
//
// Mutation: put `go test -race ./...` back, wrap the command in an echo, drop
// --race, re-add --cover, change the mode to smoke, or drop the checkout's
// fetch-depth, and this fails naming what it found.
func TestCITestJobRunsTheAccountedSuiteUnderRace(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	workflow, _ := readCIWorkflow(t, root)
	assertCIJobRunsTheAccountedSuite(t, workflow, "test", []string{"--race"}, []string{"--cover"})

	job, ok := workflow.Jobs["test"]
	if !ok {
		t.Fatal("ci.yml declares no `test` job")
	}
	var checkouts int
	for _, step := range job.Steps {
		if step.Uses != "actions/checkout@v7" {
			continue
		}
		checkouts++
		if step.With["fetch-depth"] != "0" {
			t.Fatalf("the `test` job's checkout pins fetch-depth %q; the release-baseline revision guard reads the last released registry.snapshot.json out of git and would skip [inapplicable] on every push without the full history (fetch-depth: 0)", step.With["fetch-depth"])
		}
	}
	if checkouts != 1 {
		t.Fatalf("the `test` job checks out the repository %d times; one checkout carries the fetch-depth this job's guards need", checkouts)
	}
}

// The `cover` job's suite runs with coverage and without race. It is the
// other half of the split: coverage numbers do not need the race detector's
// verdict, and the race detector does not need coverage's numbers, so the
// two run in parallel and the green wall is the slower of the two instead of
// their sum.
//
// The job needs the same database the `test` job needs, and for the same
// reason: the accounted suite refuses a skip in CI, and without a reachable
// TEST_DATABASE_URL every database fixture would skip and fail the job over
// services this job never started.
//
// Mutation: drop --cover, re-add --race, delete the job, drop the env or the
// service container, wrap the command in an echo, or change the mode to
// smoke, and this fails naming what it found.
func TestCICoverJobRunsTheAccountedSuiteUnderCover(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	workflow, goVersion := readCIWorkflow(t, root)
	job, ok := workflow.Jobs["cover"]
	if !ok {
		t.Fatal("the workflow has no cover job, so coverage has left CI: the split moved it out of `test`, not out of the workflow")
	}
	assertCIJobIsARealGate(t, "cover", job, goVersion)
	assertCIJobRunsTheAccountedSuite(t, workflow, "cover", []string{"--cover"}, []string{"--race"})
	if _, hasService := job.Services["postgres"]; !hasService {
		t.Fatal("the cover job runs no postgres service, so TEST_DATABASE_URL names a server nothing answers and the accounted suite refuses over it")
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

// eraWalkWorkflowPath is the era walk's own workflow: the cross-release
// upgrade gate (old-tag derivatives versus today's binary), owned by
// ggg/system/ci-github beside ci.yml. It is asserted here for the same
// reason ci.yml is: nobody in this repository executes it on a schedule, so
// only a test that reads it can catch the job being deleted, gated off,
// narrowed to push (which would make it a contributor gate), or checked out
// shallow (which would turn every run into the walk's own loud refusal).
const eraWalkWorkflowPath = ".github/workflows/era-walk.yml"

// ciEraWalkTest is the one test the `era-walk` job exists to run, named for
// the same reason ciGenesisSweepTest is: the walk skips itself as
// [inapplicable] unless GGG_ERA_WALK is set, and this job is the only place
// that sets it. Delete the job and the only end-to-end cross-release upgrade
// gate goes green-by-skip in every environment without one test turning red.
const ciEraWalkTest = "TestOldEraDerivativesWalkToCurrent"

// The era-walk workflow must run the walk on a full checkout, on the two
// non-contributor triggers only, in no job's needs chain, and with the env
// that un-skips the test — and it must never narrow to push/pull_request or
// grow a needs edge, either of which would quietly convert a weekly-tier
// gate into a contributor gate or a required check.
//
// The "never required" half is what the YAML can state: no needs edge
// anywhere in either workflow (ci.yml's own no-gating assertion covers its
// side), and no trigger that fires on a change. Branch protection marking
// the check required is a repository-settings act outside any file here;
// this test is the enforceable half.
//
// Mutation: delete the workflow, drop fetch-depth, drop the env, add
// push/pull_request, add a needs edge, wrap the go test in an echo, point
// -run at another test, drop -count=1, drop -timeout, or delete the runtime
// summary step, and this fails naming what it found.
func TestCIEraWalkWorkflowRunsTheEraWalk(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(eraWalkWorkflowPath)))
	if err != nil {
		t.Fatalf("read %s: %v", eraWalkWorkflowPath, err)
	}
	var workflow ciWorkflow
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse %s: %v", eraWalkWorkflowPath, err)
	}

	// The tier, stated as triggers: workflow_dispatch for on-demand runs
	// (before a release), one weekly schedule as the net under the
	// forgetting, and nothing that fires on a change. A push or pull_request
	// trigger here would put a minutes-long job on every contributor's wall
	// and break ci.yml's concurrency budget.
	if workflow.On.Kind != yaml.MappingNode {
		t.Fatalf("%s declares no trigger mapping, so nothing states when it runs", eraWalkWorkflowPath)
	}
	triggers := map[string]*yaml.Node{}
	for index := 0; index+1 < len(workflow.On.Content); index += 2 {
		triggers[workflow.On.Content[index].Value] = workflow.On.Content[index+1]
	}
	for _, refuse := range []string{"push", "pull_request"} {
		if _, ok := triggers[refuse]; ok {
			t.Fatalf("%s runs on %s; the era walk is a weekly-tier gate and must never fire on a change", eraWalkWorkflowPath, refuse)
		}
	}
	if _, ok := triggers["workflow_dispatch"]; !ok {
		t.Fatalf("%s has no workflow_dispatch trigger, so it cannot be run on demand before a release; triggers are %v",
			eraWalkWorkflowPath, maps.Keys(triggers))
	}
	schedule, ok := triggers["schedule"]
	if !ok {
		t.Fatalf("%s has no schedule trigger; without one the gate runs only when someone remembers, which is how the two original bricks shipped", eraWalkWorkflowPath)
	}
	var scheduled []struct {
		Cron string `yaml:"cron"`
	}
	if err := schedule.Decode(&scheduled); err != nil || len(scheduled) != 1 || scheduled[0].Cron == "" {
		t.Fatalf("%s declares %d usable schedule entries; the weekly tier is exactly one cron", eraWalkWorkflowPath, len(scheduled))
	}
	// No needs edge, in this file or pointed at it from ci.yml (whose own
	// assertion forbids needs entirely).
	assertCINoJobGatesOnAnother(t, eraWalkWorkflowPath, workflow)

	job, ok := workflow.Jobs["era-walk"]
	if !ok {
		t.Fatalf("%s has no era-walk job, so nothing sets GGG_ERA_WALK and %s never runs anywhere", eraWalkWorkflowPath, ciEraWalkTest)
	}
	_, goVersion := readCIWorkflow(t, root)
	assertCIJobIsARealGate(t, "era-walk", job, goVersion)

	// The full history: the walk materializes era trees with `git archive
	// <tag>`, and a default shallow checkout has no tags — the run would
	// degrade to the walk's own refusal every week, a gate that never gates.
	for _, step := range job.Steps {
		if step.Uses == "actions/checkout@v7" && step.With["fetch-depth"] != "0" {
			t.Fatalf("the era-walk checkout pins fetch-depth %q; the walk needs the tags and full history (fetch-depth: 0)", step.With["fetch-depth"])
		}
	}
	if got := job.Env["GGG_ERA_WALK"]; got != "1" {
		t.Fatalf("the era-walk job sets GGG_ERA_WALK=%q, want \"1\"; without it %s skips itself", got, ciEraWalkTest)
	}

	var found []string
	setupIndex, walkIndex, summaryIndex := -1, -1, -1
	for index, step := range job.Steps {
		if isMakeSetupStep(step) {
			setupIndex = index
		}
		for line := range strings.SplitSeq(step.Run, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			// Matched as a COMMAND by its first words, the standard the
			// rest of this file sets: an echo runs no test.
			if len(fields) < 2 || fields[0] != "go" || fields[1] != "test" {
				continue
			}
			if step.If != "" || step.ContinueOnError {
				t.Fatalf("the era-walk step is exempt from failing the build (if: %q, continue-on-error: %v)", step.If, step.ContinueOnError)
			}
			if strings.ContainsAny(strings.TrimSpace(step.Run), "\n|;&>") || strings.Contains(step.Run, "set +e") {
				t.Fatalf("the era-walk step wraps the command in shell that can hide its exit status: %q", step.Run)
			}
			found = append(found, strings.Join(fields, " "))
			walkIndex = index
		}
		if strings.Contains(step.Run, "GITHUB_STEP_SUMMARY") && step.If == "always()" {
			summaryIndex = index
		}
	}
	if len(found) != 1 {
		t.Fatalf("the era-walk job runs `go test` %d time(s): %q; want exactly one walk", len(found), found)
	}
	fields := strings.Fields(found[0])
	at := slices.Index(fields, "-run")
	if at < 0 || fields[at+1] != ciEraWalkTest {
		t.Fatalf("the era-walk command %q does not select %s by -run", found[0], ciEraWalkTest)
	}
	assertRunFilterSelectsAGGGCLITest(t, fields[at+1])
	for _, want := range []string{"-count=1", "./internal/gggcli"} {
		if !slices.Contains(fields, want) {
			t.Fatalf("the era-walk command %q is missing %s", found[0], want)
		}
	}
	// -timeout because the cold-cache walk runs minutes past go test's 10 m
	// package default; without it the job reports a timeout instead of a
	// verdict.
	timeoutAt := slices.Index(fields, "-timeout")
	if timeoutAt < 0 || timeoutAt+1 >= len(fields) {
		t.Fatalf("the era-walk command %q carries no -timeout, so a cold run dies at go test's 10m default instead of reporting a verdict", found[0])
	}
	if setupIndex < 0 || setupIndex > walkIndex {
		t.Fatal("the era-walk job runs the walk before `make setup`, so the pinned tools are absent")
	}
	if summaryIndex < 0 || summaryIndex < walkIndex {
		t.Fatal("the era-walk job reports no runtime in its step summary after the walk; the measured-cost rule applies to this job too")
	}
}

// liveCanaryWorkflowPath is the managed-target canary suite's own workflow:
// tier 2 of provider verification, the one that drives the real Resend, R2,
// Polar sandbox, Clerk, PostHog, Sentry, OpenAI-compatible, Upstash,
// Typesense, Ably, OTLP and Neon accounts this repository maintains. It is
// asserted here for the same reason era-walk.yml is: nobody executes it by
// hand on a schedule, so only a test that reads it can catch the job being
// deleted, gated off, or narrowed to push — which would put a dozen-plus
// third-party services on every contributor's wall and, worse, fail every
// fork that has none of the credentials.
const liveCanaryWorkflowPath = ".github/workflows/live-canary.yml"

// ciLiveCanaryTest is the one test the `live-canary` job exists to run. The
// suite skips itself as [inapplicable] unless GGG_LIVE_CANARY is set and this
// job is the only place that sets it, so deleting the job would take the only
// check on every believed-but-never-verified managed provider wire shape without
// one test turning red.
const ciLiveCanaryTest = "TestManagedTargetLiveCanaries"

// The live-canary workflow must run the canary suite on the two
// non-contributor triggers only, in no job's needs chain, with the env that
// un-skips the suite, and it must never narrow to push/pull_request or grow a
// needs edge — either of which would convert a weekly credential-bearing
// suite into a contributor gate or a required check.
//
// This is the same shape TestCIEraWalkWorkflowRunsTheEraWalk asserts, and
// deliberately so: the tier is the invariant, not the subject. One difference
// is asserted rather than shared — this checkout is NOT pinned to
// fetch-depth: 0, because no row reads git history and a full fetch would be
// pure cost.
//
// The complement lives with the table: internal/gggcli's
// TestEveryLiveCanaryKeyIsMappedInTheWorkflow checks that every credential a
// row declares is actually mapped here, so a new row cannot skip in CI
// forever while reporting a clean line. This test owns the tier; that one
// owns the wiring.
//
// Mutation: delete the workflow, drop the env, add push/pull_request, add a
// needs edge, gate the job or the step behind `if:`, mark either
// continue-on-error, wrap the go test in an echo or a `|| true`, point -run
// at another test, drop -count=1, drop -timeout, or delete the runtime
// summary step, and this fails naming what it found.
func TestCILiveCanaryWorkflowIsNotAContributorGate(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(liveCanaryWorkflowPath)))
	if err != nil {
		t.Fatalf("read %s: %v", liveCanaryWorkflowPath, err)
	}
	var workflow ciWorkflow
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse %s: %v", liveCanaryWorkflowPath, err)
	}

	// The tier, stated as triggers.
	if workflow.On.Kind != yaml.MappingNode {
		t.Fatalf("%s declares no trigger mapping, so nothing states when it runs", liveCanaryWorkflowPath)
	}
	triggers := map[string]*yaml.Node{}
	for index := 0; index+1 < len(workflow.On.Content); index += 2 {
		triggers[workflow.On.Content[index].Value] = workflow.On.Content[index+1]
	}
	for _, refuse := range []string{"push", "pull_request"} {
		if _, ok := triggers[refuse]; ok {
			t.Fatalf("%s runs on %s; the live canaries need credentials no contributor and no fork has, so firing on a change would fail every outside change and put every maintained managed provider on the contributor wall", liveCanaryWorkflowPath, refuse)
		}
	}
	if _, ok := triggers["workflow_dispatch"]; !ok {
		t.Fatalf("%s has no workflow_dispatch trigger, so it cannot be run on demand before a release; triggers are %v",
			liveCanaryWorkflowPath, maps.Keys(triggers))
	}
	schedule, ok := triggers["schedule"]
	if !ok {
		t.Fatalf("%s has no schedule trigger; the drift this suite catches is a provider moving under us, which no change to this repository will ever trigger, so without a schedule it runs when someone remembers", liveCanaryWorkflowPath)
	}
	var scheduled []struct {
		Cron string `yaml:"cron"`
	}
	if err := schedule.Decode(&scheduled); err != nil || len(scheduled) != 1 || scheduled[0].Cron == "" {
		t.Fatalf("%s declares %d usable schedule entries; the weekly tier is exactly one cron", liveCanaryWorkflowPath, len(scheduled))
	}
	assertCINoJobGatesOnAnother(t, liveCanaryWorkflowPath, workflow)

	job, ok := workflow.Jobs["live-canary"]
	if !ok {
		t.Fatalf("%s has no live-canary job, so nothing sets GGG_LIVE_CANARY and %s never runs anywhere", liveCanaryWorkflowPath, ciLiveCanaryTest)
	}
	_, goVersion := readCIWorkflow(t, root)
	assertCIJobIsARealGate(t, "live-canary", job, goVersion)

	if got := job.Env["GGG_LIVE_CANARY"]; got != "1" {
		t.Fatalf("the live-canary job sets GGG_LIVE_CANARY=%q, want \"1\"; without it %s skips itself and the job is a green report on nothing", got, ciLiveCanaryTest)
	}
	// The one selector the suite refuses rather than skips. Pinned to the
	// literal here so a misconfigured repository secret cannot present it.
	if got := job.Env["POLAR_SERVER"]; got != "sandbox" {
		t.Fatalf("the live-canary job sets POLAR_SERVER=%q, want \"sandbox\"; the Polar probe creates a checkout session and ingests an immutable metered event, and this pin is what keeps a repository-secret mistake away from a production tenant", got)
	}

	var found []string
	setupIndex, canaryIndex, summaryIndex := -1, -1, -1
	for index, step := range job.Steps {
		if isMakeSetupStep(step) {
			setupIndex = index
		}
		for line := range strings.SplitSeq(step.Run, "\n") {
			fields := strings.Fields(strings.TrimSpace(line))
			// Matched as a COMMAND by its first words, the standard the rest
			// of this file sets: an echo runs no test.
			if len(fields) < 2 || fields[0] != "go" || fields[1] != "test" {
				continue
			}
			if step.If != "" || step.ContinueOnError {
				t.Fatalf("the live-canary step is exempt from failing the build (if: %q, continue-on-error: %v); every row already skips itself when its credentials are absent, so there is nothing left for an exemption to buy", step.If, step.ContinueOnError)
			}
			if strings.ContainsAny(strings.TrimSpace(step.Run), "\n|;&>") || strings.Contains(step.Run, "set +e") {
				t.Fatalf("the live-canary step wraps the command in shell that can hide its exit status: %q", step.Run)
			}
			found = append(found, strings.Join(fields, " "))
			canaryIndex = index
		}
		if strings.Contains(step.Run, "GITHUB_STEP_SUMMARY") && step.If == "always()" {
			summaryIndex = index
		}
	}
	if len(found) != 1 {
		t.Fatalf("the live-canary job runs `go test` %d time(s): %q; want exactly one suite", len(found), found)
	}
	fields := strings.Fields(found[0])
	at := slices.Index(fields, "-run")
	if at < 0 || fields[at+1] != ciLiveCanaryTest {
		t.Fatalf("the live-canary command %q does not select %s by -run", found[0], ciLiveCanaryTest)
	}
	assertRunFilterSelectsATest(t, "canary", fields[at+1])
	for _, want := range []string{"-count=1", "./internal/canary"} {
		if !slices.Contains(fields, want) {
			t.Fatalf("the live-canary command %q is missing %s", found[0], want)
		}
	}
	// -v because a run whose rows all skipped and a run whose rows all passed
	// are the same exit code; the reasoned skip lines are the report.
	if !slices.Contains(fields, "-v") {
		t.Fatalf("the live-canary command %q is missing -v, so the per-provider [inapplicable] reasons — the only record of which providers were actually checked — never reach the log", found[0])
	}
	// -timeout because every row's own 30s deadline, times the row count, plus a cold
	// build can pass go test's 10m package default.
	if timeoutAt := slices.Index(fields, "-timeout"); timeoutAt < 0 || timeoutAt+1 >= len(fields) {
		t.Fatalf("the live-canary command %q carries no -timeout, so a slow provider run dies at go test's 10m default instead of reporting a verdict", found[0])
	}
	if setupIndex < 0 || setupIndex > canaryIndex {
		t.Fatal("the live-canary job runs the suite before `make setup`, so the pinned tools are absent")
	}
	if summaryIndex < 0 || summaryIndex < canaryIndex {
		t.Fatal("the live-canary job reports no runtime in its step summary after the suite; the measured-cost rule applies to this job too")
	}
}

// The workflow must cancel superseded runs. The parallel layout trades
// fail-fast compute economics for wall-time, and that trade is only sound
// because a superseded push stops burning runners: without cancel-in-progress
// every push runs the whole nine-job matrix to completion, superseded or not,
// and the economics quietly invert. The group keys on the workflow and the
// ref, so pushes to main supersede pushes to main and a PR's pushes supersede
// that PR — never each other.
//
// Mutation: delete the block, drop cancel-in-progress, or key the group on
// the run id (which never collides and so cancels nothing), and this fails.
func TestCIWorkflowCancelsSupersededRuns(t *testing.T) {
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	workflow, _ := readCIWorkflow(t, root)
	if workflow.Concurrency.Group == "" {
		t.Fatalf("%s declares no concurrency group, so superseded pushes run every job to completion", ciWorkflowPath)
	}
	if !workflow.Concurrency.CancelInProgress {
		t.Fatalf("%s declares a concurrency group without cancel-in-progress: a queued superseded run still executes in full, which is the compute the parallel layout traded on", ciWorkflowPath)
	}
	for _, part := range []string{"${{ github.workflow }}", "${{ github.ref }}"} {
		if !strings.Contains(workflow.Concurrency.Group, part) {
			t.Fatalf("%s keys its concurrency group on %q, which does not mention %s; a group that never collides cancels nothing", ciWorkflowPath, workflow.Concurrency.Group, part)
		}
	}
}

// gggcliTestNames is every top-level `func TestXxx(t *testing.T)` declared in
// internal/gggcli, read out of the package's own sources. It is the
// population `go test -run` filters, derived rather than written down.
func gggcliTestNames(t *testing.T) []string { return packageTestNames(t, "gggcli") }

// packageTestFloors is the minimum number of top-level tests each swept
// package must declare. The floor is per-package because it exists to catch a
// walk that found the WRONG DIRECTORY — which answers zero for every pattern
// and turns the guard into the vacuity it refuses — and internal/gggcli
// declares hundreds where internal/canary declares the suite plus its five
// guards. A shared floor would either be vacuous for the large package or
// unsatisfiable for the small one.
var packageTestFloors = map[string]int{"gggcli": 50, "canary": 5}

func packageTestNames(t *testing.T, pkg string) []string {
	t.Helper()
	dir := filepath.Join("..", pkg)
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
	floor, known := packageTestFloors[pkg]
	if !known {
		t.Fatalf("package %q has no packageTestFloors entry; a swept package with no floor cannot report a collapsed walk", pkg)
	}
	if len(names) < floor {
		t.Fatalf("only %d top-level tests were read out of %s, want at least %d; the walk has collapsed, not the package", len(names), dir, floor)
	}
	return names
}

// assertRunFilterSelectsAGGGCLITest refuses a `-run` pattern that selects no
// test. This is the vacuity `go test` itself will not report: an unmatched
// filter is a warning on stdout and exit 0, so a CI job pinned to a renamed
// test passes while running nothing.
func assertRunFilterSelectsAGGGCLITest(t *testing.T, pattern string) {
	t.Helper()
	assertRunFilterSelectsATest(t, "gggcli", pattern)
}

// assertRunFilterSelectsATest is the same check over any internal package
// directory. It became a parameter when the live-canary suite moved out of
// internal/gggcli: internal/gggcli may not name an adapter package at all
// (ValidateCoreCLIPackages), and a canary over every managed adapter must.
func assertRunFilterSelectsATest(t *testing.T, pkg, pattern string) {
	t.Helper()
	names := packageTestNames(t, pkg)
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

// assertCIJobIsARealGate checks the wiring every job in this workflow shares,
// and that nothing exempts the job from failing the build.
func assertCIJobIsARealGate(t *testing.T, name string, job ciJob, goVersion string) {
	t.Helper()
	if job.If != "" {
		t.Fatalf("job %s is gated by if: %q, so it can be skipped silently", name, job.If)
	}
	if job.ContinueOnError {
		t.Fatalf("job %s is continue-on-error, so a failed gate would not fail the build", name)
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
	assertCINoJobGatesOnAnother(t, ciWorkflowPath, workflow)
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

// assertCINoJobGatesOnAnother holds the workflow's parallel layout: no job
// declares `needs`. Every job checks out and builds its own tree and none
// consumes another's artifacts, so a `needs:` edge buys fail-fast at the
// price of serialising the matrix behind the longest job — the 24m41s green
// wall this workflow carried while seven jobs waited on `test` was exactly
// that. Re-adding a gate is a budget decision: state it beside the
// workflow-level concurrency comment, not silently here. It runs inside
// readCIWorkflow so it reaches every job, including the ones no per-job
// assertion visits, and from the two weekly-tier workflows' own guards, for
// which a needs edge would additionally mean an accidentally-required check.
//
// The path is a parameter rather than the ci.yml constant because three
// workflows are checked through here: a refusal that named ci.yml while
// reading live-canary.yml sends the reader to the wrong file.
func assertCINoJobGatesOnAnother(t *testing.T, path string, workflow ciWorkflow) {
	t.Helper()
	names := make([]string, 0, len(workflow.Jobs))
	for name := range workflow.Jobs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if needs := workflow.Jobs[name].Needs; len(needs) > 0 {
			t.Fatalf("job %s needs %v; no job in %s gates on another (each checks out and builds fresh), so the green wall is the slowest job instead of a chain. Re-adding a gate is a budget decision — say it beside the concurrency comment",
				name, needs, path)
		}
	}
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
	On yaml.Node `yaml:"on"`
	// Concurrency is the supersession group: without it, every push runs the
	// whole now-parallel matrix to completion, superseded or not.
	Concurrency ciConcurrency    `yaml:"concurrency"`
	Jobs        map[string]ciJob `yaml:"jobs"`
}

type ciConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
}

type ciJob struct {
	// Needs is asserted EMPTY: no job in this workflow gates on another.
	Needs           ciStringList      `yaml:"needs"`
	RunsOn          string            `yaml:"runs-on"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	Services        map[string]struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
	Steps []ciStep `yaml:"steps"`
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
