package gggcli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The profile gate matrix, and where each row runs.
//
// Only one profile was ever exercised end to end — `saas` — and that is how
// three broken profiles shipped for a release: `web` and the since-deleted
// `api` were refused by the resolver's own provider-slot check, and `minimal`
// wrote a whole tree and then died in `go mod tidy` because
// `internal/web/routes.go` imported a package no module in its closure owned.
// Every shipped profile is now covered by three rows:
//
//	| row                                                      | per profile | where |
//	|----------------------------------------------------------|-------------|-------|
//	| plan + two coherence properties over the planned bytes    | ~1.3 s      | `go test ./internal/modkit`, so `make check` |
//	| documented table, description and slot counts             | ~0.9 s      | same |
//	| real `ggg new`, `sync --check --offline`, then the created |             | |
//	| tree's own `go build ./...` and test-payload compile       | ~34 s       | this test, CI's `profiles` job |
//
// The first two rows are `internal/modkit/shipped_profiles_test.go`. They
// prove everything the planner and the generators decide with no toolchain, no
// network and no Docker, and they are deliberately the rows that fail in
// `go test` on the commit that breaks a profile. What they cannot prove is
// that the external tools succeed and that the tree they wrote compiles,
// which is what the third row is for.
//
// # What "a project" means, and why exit 0 is not it
//
// Three commands, in the order an operator runs them:
//
//	ggg new              the tree is written
//	sync --check         its own engine reports no pending change and no drift
//	go build ./...       the product code compiles
//	go test -run …       the TEST PAYLOADS compile
//
// The last row is here because it shipped broken. A `ggg new --profile
// ggg/profile/minimal` project built cleanly and `go test ./...` — plausibly
// the first command a user runs next — did not compile: 184 type errors across
// eleven test payloads, every one of them a payload naming a symbol another
// module owns with no declared edge to it. `go build` cannot see it because it
// never compiles a `_test.go`, and `sync --check` cannot see it because the
// bytes on disk are exactly the bytes the manifests declare. The tree was
// wrong and every gate that ran over it was green.
//
// `go test -run XXXNONE ./...` rather than `go vet ./...`, measured on the
// same tree: vet reported 3 errors and the compiler reported 184. vet stops at
// the first type error in a package, so it named one symbol per package and
// hid the rest; `go test -run` performs exactly the build `go test ./...`
// performs — every test binary compiled and LINKED, no test run — and reports
// up to ten per package. It also reuses the object files `go build ./...` just
// produced, so the second command costs the test files and the link only.
//
// # Cost, measured
//
// Apple M1 Max, warm Go module cache, `--registry directory:.`, one run of
// this test:
//
//	profile   ggg new    sync --check   go build   test compile   closure
//	minimal   14.5 s     0.9 s          1.5 s      14.4 s         159
//	web       20.2 s     1.4 s          3.3 s      16.1 s         254
//	saas      26.5 s     1.6 s          2.1 s      24.1 s         289
//	full      21.0 s     1.8 s          2.7 s      24.4 s         288
//
// 176 s for the four plus the binary under test: 180 s wall, up from ~100 s.
// The two compile columns are 79 s of that, and they are what makes the other
// two mean "a project" rather than "some files": on the commit before this one
// every profile above was green through `sync --check` and `minimal`'s test
// compile reported 181 errors across eleven payloads.
//
// The build cache in each destination is cold, which is most of the 79 s. It
// is not worth warming: a shared cache across four different module paths is
// what would make one profile's result depend on another's.
//
// # Why it is not a step of `make check`
//
// Not the 180 s — `make check` already runs the generators and the whole Go
// suite and would absorb it. It is that genesis runs `go mod tidy` in a
// destination outside this module's tree and installs the pinned Tailwind
// binary, so the sweep needs the NETWORK. `ggg check` has to stay runnable
// offline; a gate that goes red because a proxy hiccuped is a gate
// contributors learn to ignore, and this repository has already paid that
// price once with a suite whose skips read as passes.
//
// That is the same reason `ggg registry validate` — which likewise installs,
// compiles and tests a real derivative — is two dedicated CI jobs rather than
// a `make check` step, and this follows that precedent rather than inventing
// a third convention. It is opt-in through GGG_GENESIS_SWEEP=1; CI's
// `profiles` job sets it, and the skip everywhere else carries
// InapplicableSkipMarker because the claim is checked once by the job that
// owns it and paying 180 s again in the `test` job buys nothing.
//
// The four profiles stay in one job. They are four independent `t.Run`s over
// disjoint temporary directories, so splitting them per profile would buy
// ~110 s of wall clock at the cost of four `make setup` runs and four Go build
// caches; the job is not the long pole in this repository's CI and a matrix
// here would make the shortest job the one that has to warm a cache.
//
// # The profile list is read, not written
//
// All three rows walk `catalog.Profiles`. A fifth profile is swept with no
// edit here, which is exactly what the hand-maintained table in
// content/docs/getting-started.md did not have and why it advertised a
// profile the catalog had stopped publishing.
func TestEveryShippedProfileCreatesAProjectThatIsSyncClean(t *testing.T) {
	root := repoRootFromTest(t)
	catalog, err := modkit.LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatalf("loading the core catalog: %v", err)
	}
	if len(catalog.Profiles) == 0 {
		t.Fatal("the core catalog advertises no profiles")
	}
	if os.Getenv("GGG_GENESIS_SWEEP") != "1" {
		t.Skipf("%s the four-profile genesis sweep is CI's `profiles` job (it needs the network); "+
			"set GGG_GENESIS_SWEEP=1 to run it here", InapplicableSkipMarker)
	}

	ggg := filepath.Join(t.TempDir(), "ggg")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", ggg, "./cmd/ggg")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the binary under test: %v\n%s", err, out)
	}

	for _, profile := range catalog.Profiles {
		t.Run(profile.Name, func(t *testing.T) {
			// A path under t.TempDir() that `ggg new` creates itself: the
			// command refuses a destination that already exists, and on
			// failure it removes only what it created.
			dest := filepath.Join(t.TempDir(), "genesis")
			started := time.Now()
			runGGG(t, ggg, root, "new", dest,
				"--module", "example.com/genesis"+strings.ReplaceAll(profile.Name, "-", ""),
				"--profile", profile.ID, "--registry", "directory:.", "--non-interactive")
			created := time.Since(started)

			// Exit 0 from `ggg new` is not the claim. The first thing an
			// operator does in a new tree is a sync, and a genesis whose own
			// engine then reports a pending change or generated drift over
			// the tree it just wrote has not produced a project. Offline
			// because the destination's lock has to be self-sufficient: the
			// catalog was vendored under _registry-core, and a sync that
			// reached the network here would be checking something else.
			started = time.Now()
			runGGG(t, ggg, dest, "sync", "--check", "--offline")
			checked := time.Since(started)

			// The count the profile's own description advertises is checked
			// against the PLANNER by row one. Checking it here against the
			// lock genesis actually wrote is what makes the rows one claim
			// rather than two: if they ever disagree, the cheap rows are
			// planning something the expensive row does not build.
			lock, err := modkit.ParseLock(readTestFile(t, dest, modkit.LockFileName))
			if err != nil {
				t.Fatalf("parsing the created project's lock: %v", err)
			}
			stated := advertisedModuleCount.FindStringSubmatch(profile.Description)
			if stated == nil {
				t.Fatalf("%s's description states no module count", profile.ID)
			}
			want, err := strconv.Atoi(stated[1])
			if err != nil {
				t.Fatal(err)
			}
			if len(lock.Modules) != want {
				t.Fatalf("%s installed %d modules; its own description advertises %d",
					profile.ID, len(lock.Modules), want)
			}

			// The tree compiles, both halves. `go build ./...` never reads a
			// _test.go, and the test payloads are installed source too: a
			// payload that names a symbol another module owns leaves
			// `go test ./...` — plausibly the next command a user runs —
			// refusing to compile in a tree every earlier gate called clean.
			//
			// -gcflags=-e lifts the compiler's ten-errors-per-package cap, so
			// one run names every undeclared reference rather than the first
			// of each package. -run XXXNONE matches no test, so this is the
			// build and the link and nothing else.
			started = time.Now()
			runIn(t, dest, "go", "build", "./...")
			built := time.Since(started)
			started = time.Now()
			runIn(t, dest, "go", "test", "-run", "XXXNONE", "-gcflags=-e", "./...")
			compiled := time.Since(started)

			t.Logf("%s: ggg new %.1fs, sync --check %.1fs, go build %.1fs, test compile %.1fs, %d modules",
				profile.ID, created.Seconds(), checked.Seconds(),
				built.Seconds(), compiled.Seconds(), len(lock.Modules))
		})
	}
}

var advertisedModuleCount = regexp.MustCompile(`(\d+) modules`)

// runGGG runs one command through the built binary and fails with everything
// it printed. `ggg new`'s refusals are the interesting failures here and they
// carry their cause on stderr, so discarding either stream would turn a
// diagnosable failure into "exit 3".
func runGGG(t *testing.T, binary, dir string, argv ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, argv...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err == nil {
		return
	}
	exit := -1
	var coded *exec.ExitError
	if errors.As(err, &coded) {
		exit = coded.ExitCode()
	}
	t.Fatalf("ggg %s (in %s) = exit %d: %v\n%s", strings.Join(argv, " "), dir, exit, err, out)
}

// runIn runs one toolchain command in the created tree and fails with
// everything it printed. The compiler's diagnostics ARE the failure here — a
// bare "exit 1" would say a profile is broken without saying which payload
// names which missing symbol — so both streams are kept.
func runIn(t *testing.T, dir, binary string, argv ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, argv...)
	command.Dir = dir
	out, err := command.CombinedOutput()
	if err == nil {
		return
	}
	t.Fatalf("%s %s (in %s) = %v\n%s", binary, strings.Join(argv, " "), dir, err, out)
}

// The CI job that sets GGG_GENESIS_SWEEP is asserted in
// internal/modkit/ci_workflow_test.go, beside every other claim about this
// repository's own workflow, by TestCIProfilesJobRunsTheGenesisSweep. That
// file already parses the YAML and rejects an `if:`, a continue-on-error, a
// wrapped command and a missing `make setup`; a substring grep here would be
// a second, weaker convention for the same question.
