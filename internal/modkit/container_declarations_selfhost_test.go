// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS
// repository's own manifests — never about the source the registry
// distributes.

package modkit

// Every container this repository DECLARES, executed.
//
// The declarations this file reads had never been run by anything. Containers
// reached Docker from three places — CI service blocks, `ggg services up`,
// and scripts/visual-run.sh — and none of the three consulted a manifest, so
// `dependencies.containers` and `local_service` were metadata that only ever
// had to parse. Running them once, by hand, found three defects in the two
// declarations nobody had ever started:
//
//   - ggg/system/storage-s3 pinned `minio/minio@sha256:fce0a90a…`, and MinIO
//     has since removed that Docker Hub repository: the pull fails with
//     "repository does not exist" and hub.docker.com answers "object not
//     found". Every project selecting that target had a broken
//     `ggg services up` and nothing said so. (The same digest is still served
//     by quay.io, which is where the declaration now points.)
//   - its declared health command was
//     `wget -qO- http://127.0.0.1:9000/minio/health/live`, and that image
//     contains no wget — only `mc`. A generated Compose healthcheck naming a
//     binary the image does not have can never pass, so the service would
//     have sat unhealthy forever and `depends_on: service_healthy` would have
//     hung the app.
//   - and then the one the first two exposed: `LocalService` had no field for
//     a container command, so the `server /data` that image needs could not
//     be declared at all and GenerateComposeFiles emitted no `command:`. The
//     service was unstartable BY SCHEMA — no manifest edit could fix it. That
//     gap was held here by a named `unstartableLocalServices` row until
//     LocalService.Command closed it; the row is gone, and this gate now has
//     no exemption path at all. A declaration that cannot start is red.
//
// All three are mechanical, and none needed a person: one resolves a digest,
// the others run one command in one image. So this is that check, over every
// declaration rather than the two somebody happened to touch.
//
// It needs Docker and the network, so it is off by default with a declared
// skip and on in CI's `test` job — which already has all three images on the
// runner for its own services.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// containerDeclarationsEnv un-skips this gate, the same way GGG_ERA_WALK and
// GGG_GENESIS_SWEEP un-skip theirs.
const containerDeclarationsEnv = "GGG_CONTAINER_DECLARATIONS"

// declaredContainer is one container declaration with the manifest coordinates
// to name it by in a failure.
type declaredContainer struct {
	Owner   string
	Where   string
	Image   string
	Target  string
	Command string
	Env     []LocalServiceEnv
	Health  *LocalServiceHealth
}

// key identifies one declaration in a subtest name and a failure.
func (d declaredContainer) key() string { return d.Owner + "@" + d.Target }

func TestEveryDeclaredContainerIsExecutable(t *testing.T) {
	if os.Getenv(containerDeclarationsEnv) == "" {
		t.Skipf("[inapplicable] executing every declared container needs Docker and the network to resolve each pinned "+
			"digest and run each declared health probe in its own image (measured 19 s warm, 16 s of it registry "+
			"round-trips, plus 179 MB of compressed layers on a cold cache); CI's `test` job owns it and sets %s=1 "+
			"beside the postgres, mailpit and minio containers it already starts — export %s=1 to run it here",
			containerDeclarationsEnv, containerDeclarationsEnv)
	}
	root, err := canonicalProjectRoot(specRepoRoot(t))
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	// Docker is NOT optional once the variable is set, and that is the same
	// rule internal/db/testdb/testdb.go:103-138 draws: an absent variable is
	// an absence, and a variable somebody exported is a REQUEST, so a request
	// this machine cannot serve is a failure rather than a quiet pass.
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("%s is set but docker is not on PATH: %v\nUnset %s to let this gate skip.",
			containerDeclarationsEnv, err, containerDeclarationsEnv)
	}

	declarations := declaredContainers(t, root)
	// The floor. This gate's whole value is coverage of declarations nobody
	// runs, so a walk that finds none of them has broken rather than the tree.
	if len(declarations) == 0 {
		t.Fatalf("no container declaration was found under %s/registry/modules; the walk has collapsed, not the tree", root)
	}
	t.Logf("%d container declaration(s) across this repository's manifests", len(declarations))

	t.Run("every pinned digest resolves at its declared host", func(t *testing.T) {
		// The first defect, mechanised. A digest is only immutable at a host
		// that still serves it, and a vanished repository is indistinguishable
		// from a healthy pin by reading the manifest. `manifest inspect`
		// fetches the manifest and no layer, so this costs one request per
		// distinct image even on a cold cache.
		for _, image := range distinctImages(declarations) {
			t.Run(image, func(t *testing.T) {
				out, err := exec.Command("docker", "manifest", "inspect", image).CombinedOutput()
				if err != nil {
					t.Fatalf("declared by %s\n`docker manifest inspect %s` failed: %v\n%s\n"+
						"A pinned digest is only immutable at a host that still serves it. Point the declaration at a "+
						"registry that has these exact bytes, or re-pin it.",
						strings.Join(ownersOf(declarations, image), ", "), image, err, strings.TrimSpace(string(out)))
				}
			})
		}
	})

	t.Run("every declared health probe can run in its image", func(t *testing.T) {
		// The second defect, mechanised. A Compose healthcheck is
		// `["CMD-SHELL", command]`, which docker runs as `/bin/sh -c command`
		// inside the container — so the probe needs BOTH a shell and the
		// binary it names, and one `command -v` proves both at once. No
		// container has to be started for this, which is why it runs over
		// every declaration including the one that cannot start.
		for _, declaration := range declarations {
			if declaration.Health == nil {
				continue
			}
			binary := probeBinary(declaration.Health.Command)
			if binary == "" {
				continue
			}
			t.Run(declaration.key()+" "+binary, func(t *testing.T) {
				out, err := exec.Command("docker", "run", "--rm", "--entrypoint", "/bin/sh",
					declaration.Image, "-c", "command -v "+binary).CombinedOutput()
				if err != nil {
					t.Fatalf("%s declares health command %q for target %s, and %q is not runnable in %s: %v\n%s\n"+
						"A Compose healthcheck is CMD-SHELL, so it needs /bin/sh AND this binary inside the image. "+
						"Name one the image has (`docker run --rm --entrypoint /bin/sh IMAGE -c 'ls /usr/bin'`).",
						declaration.Owner, declaration.Health.Command, declaration.Target, binary, declaration.Image,
						err, strings.TrimSpace(string(out)))
				}
			})
		}
	})

	t.Run("every declared health probe exits 0 against a running instance", func(t *testing.T) {
		// The whole claim, end to end: start the container from NOTHING BUT
		// its declaration — declared image, declared command, declared
		// literal environment — and poll the declared probe. A probe that
		// exists but never returns 0 leaves the service unhealthy forever,
		// and `depends_on: service_healthy` turns that into a hung
		// `ggg services up` rather than an error.
		//
		// There is no exemption list. There was one, for the single
		// declaration the schema could not express a command for, and
		// LocalService.Command retired it: every declaration that names a
		// probe is started here, and one that cannot start is red.
		for _, declaration := range declarations {
			if declaration.Health == nil || declaration.Health.Command == "" {
				continue
			}
			t.Run(declaration.key(), func(t *testing.T) {
				assertProbeGoesHealthy(t, declaration)
			})
		}
	})
}

// assertProbeGoesHealthy starts one declaration and polls its declared probe
// to a bounded deadline, then reports the container's own logs on timeout —
// without them a red line here says only that something did not come up.
func assertProbeGoesHealthy(t *testing.T, declaration declaredContainer) {
	t.Helper()
	args := []string{"run", "-d", "--rm"}
	for _, variable := range declaration.Env {
		// from_key is legal in the schema and absent from every manifest
		// here; it names a value only Compose's own expansion can read, so
		// there is nothing this process could substitute for it.
		if variable.FromKey != "" {
			t.Skipf("%s takes %s from another key, which only Compose can expand", declaration.key(), variable.Key)
		}
		args = append(args, "-e", variable.Key+"="+variable.Value)
	}
	args = append(args, declaration.Image)
	// The declared argv, appended exactly the way the generated Compose file
	// carries it: one literal token per element, no shell. An image whose own
	// CMD is not a runnable service starts only from this.
	args = append(args, strings.Fields(declaration.Command)...)
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: `docker %s` failed: %v\n%s", declaration.key(), strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	container := strings.TrimSpace(string(out))
	defer func() {
		_ = exec.Command("docker", "rm", "-f", container).Run()
	}()

	deadline := time.Now().Add(30 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		probe, probeErr := exec.Command("docker", "exec", container, "/bin/sh", "-c", declaration.Health.Command).CombinedOutput()
		if probeErr == nil {
			return
		}
		last = probe
		time.Sleep(500 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", "--tail", "40", container).CombinedOutput()
	t.Fatalf("%s: declared health command %q never exited 0 within 30s.\nlast probe output: %s\ncontainer logs:\n%s\n"+
		"A declared probe that never succeeds leaves the generated Compose service unhealthy forever, and anything "+
		"waiting on service_healthy hangs instead of failing.",
		declaration.key(), declaration.Health.Command, strings.TrimSpace(string(last)), strings.TrimSpace(string(logs)))
}

// declaredContainers reads every manifest in this repository's registry and
// returns both places a container can be declared. Decoding through the typed
// model rather than a loose map is what makes a renamed field a compile
// failure instead of an empty result that reports a clean sweep.
func declaredContainers(t *testing.T, root string) []declaredContainer {
	t.Helper()
	var found []declaredContainer
	for _, include := range catalogIncludes {
		if include.kind == CatalogProfile {
			continue
		}
		dir := filepath.Join(root, "registry", "modules", string(include.kind))
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("scan %s: %v", dir, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name(), "module.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var document ModuleDocument
			if err := decodeStrict(raw, &document); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			module := document.Module
			for _, container := range module.Dependencies.Containers {
				found = append(found, declaredContainer{
					Owner:  module.ID,
					Where:  "dependencies.containers",
					Target: "dependencies:" + container.Name,
					Image:  container.Image,
				})
			}
			system := module.Runtime.System
			if system == nil || system.Adapter == nil {
				continue
			}
			for _, target := range system.Adapter.Targets {
				if target.LocalService == nil {
					continue
				}
				health := target.LocalService.Health
				found = append(found, declaredContainer{
					Owner:   module.ID,
					Where:   "local_service",
					Target:  target.ID,
					Image:   target.LocalService.Container,
					Command: target.LocalService.Command,
					Env:     target.LocalService.Environment,
					Health:  &health,
				})
			}
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Owner != found[j].Owner {
			return found[i].Owner < found[j].Owner
		}
		return found[i].Target < found[j].Target
	})
	return found
}

func distinctImages(declarations []declaredContainer) []string {
	seen := map[string]bool{}
	var images []string
	for _, declaration := range declarations {
		if seen[declaration.Image] {
			continue
		}
		seen[declaration.Image] = true
		images = append(images, declaration.Image)
	}
	sort.Strings(images)
	return images
}

func ownersOf(declarations []declaredContainer, image string) []string {
	seen := map[string]bool{}
	var owners []string
	for _, declaration := range declarations {
		if declaration.Image != image {
			continue
		}
		label := fmt.Sprintf("%s (%s)", declaration.Owner, declaration.Where)
		if seen[label] {
			continue
		}
		seen[label] = true
		owners = append(owners, label)
	}
	sort.Strings(owners)
	return owners
}

// probeBinary is the executable a health command names. A shell builtin or a
// pipeline would not survive this reduction, and no declaration uses one; the
// point is the first word, because that is what has to exist in the image.
func probeBinary(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
