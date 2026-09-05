package gggcli

import (
	"slices"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// A task that serves or supervises the application itself must never leave the
// compose app service running. `ggg test e2e` brought the whole test stack up,
// and the unreachable app container it started ran a SECOND jobs worker
// against the same test database: two workers claiming from one SKIP-LOCKED
// queue while writing to two separate object stores, so an export CSV landed
// in whichever process won the claim and the other answered 500 for the key
// both of them could see. That failed export.spec.ts 33% of serial runs.
// `ggg dev` has the same shape and is worse — development publishes the app
// port, so the container and the supervised air process collide outright.
func TestComposeUpKeepsTheAppServiceOutOfTasksThatOwnTheAppProcess(t *testing.T) {
	for _, environment := range []string{"development", "test"} {
		argv := composeUpArgv(environment, appRunsInTask)
		if !slices.Contains(argv, modkit.ComposeFileName(environment)) {
			t.Fatalf("%s: wrong compose file: %v", environment, argv)
		}
		i := slices.Index(argv, "--scale")
		if i < 0 || i+1 >= len(argv) || argv[i+1] != "app=0" {
			t.Fatalf("%s: app service not scaled to zero: %v", environment, argv)
		}
	}
}

// Scaling, not a service allowlist. Naming the wanted services would DISABLE
// the app service, and compose then rejects `--scale app=0` outright ("no such
// service: app: disabled") while leaving an already-running app container up —
// the exact state the scale exists to clear. So the invocation must carry no
// service operand at all.
func TestComposeUpNamesNoServiceOperand(t *testing.T) {
	argv := composeUpArgv("test", appRunsInTask)
	if got := argv[len(argv)-1]; got != "app=0" {
		t.Fatalf("service operand after --scale: %v", argv)
	}
}

// `ggg services up` and `ggg db reset` own no app process of their own:
// standing the whole stack up in docker is the former's entire purpose, and
// the latter restores exactly the stack it tore down.
func TestComposeUpLeavesTheWholeStackAloneWhenTheStackOwnsTheApp(t *testing.T) {
	argv := composeUpArgv("development", appRunsInStack)
	if slices.Contains(argv, "--scale") {
		t.Fatalf("whole-stack up should not scale anything: %v", argv)
	}
	for _, want := range []string{"up", "-d", "--wait", modkit.ComposeFileName("development")} {
		if !slices.Contains(argv, want) {
			t.Fatalf("whole-stack up missing %q: %v", want, argv)
		}
	}
}
