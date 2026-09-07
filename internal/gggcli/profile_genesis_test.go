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
//	| real `ggg new`, then `sync --check --offline` in the tree  | ~22 s       | this test, CI's `profiles` job |
//
// The first two rows are `internal/modkit/shipped_profiles_test.go`. They
// prove everything the planner and the generators decide with no toolchain, no
// network and no Docker, and they are deliberately the rows that fail in
// `go test` on the commit that breaks a profile. What they cannot prove is
// that the external tools succeed, which is what the third row is for.
//
// # Cost, measured
//
// Apple M1 Max, warm Go module cache, `--registry directory:.`:
//
//	profile   ggg new    sync --check   closure
//	minimal   19.9 s     0.96 s         178
//	web       18.2 s     1.43 s         254
//	saas      27.2 s     1.56 s         289
//	full      22.0 s     1.47 s         288
//
// 93 s for the four, plus ~7 s to build the binary under test: ~100 s.
//
// # Why it is not a step of `make check`
//
// Not the 100 s — `make check` already runs the generators and the whole Go
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
// owns it and paying 100 s again in the `test` job buys nothing.
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
			t.Logf("%s: ggg new %.1fs, sync --check %.1fs, %d modules",
				profile.ID, created.Seconds(), checked.Seconds(), len(lock.Modules))
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

// The CI job that sets GGG_GENESIS_SWEEP is asserted in
// internal/modkit/ci_workflow_test.go, beside every other claim about this
// repository's own workflow, by TestCIProfilesJobRunsTheGenesisSweep. That
// file already parses the YAML and rejects an `if:`, a continue-on-error, a
// wrapped command and a missing `make setup`; a substring grep here would be
// a second, weaker convention for the same question.
