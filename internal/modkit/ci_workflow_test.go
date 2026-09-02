package modkit

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		case strings.Contains(step.Run, "make setup"):
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

// assertCIJobValidates finds the one validate invocation in the job and checks
// it is a bare command for the expected family. Bare is the point: a pipe, a
// `|| true`, a `set +e` or an `if:` on the step would all leave a green job
// over a failed closure.
func assertCIJobValidates(t *testing.T, name string, job ciJob, family ClosureFamily) {
	t.Helper()
	var invocations []ciStep
	setupIndex, validateIndex := -1, -1
	for index, step := range job.Steps {
		if strings.Contains(step.Run, "make setup") {
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

// readCIWorkflow parses the workflow and returns the toolchain the test job
// pins, so a Go bump stays a one-place edit in the workflow itself.
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

type ciWorkflow struct {
	Jobs map[string]ciJob `yaml:"jobs"`
}

type ciJob struct {
	Needs           ciStringList `yaml:"needs"`
	RunsOn          string       `yaml:"runs-on"`
	If              string       `yaml:"if"`
	ContinueOnError bool         `yaml:"continue-on-error"`
	Steps           []ciStep     `yaml:"steps"`
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
