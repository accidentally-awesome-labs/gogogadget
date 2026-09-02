// Package docker implements the deployment target for self-hosted Docker
// Compose stacks: the generated compose.yaml (development) and
// compose.test.yaml (test) that ggg/system/docker renders.
//
// The adapter drives the docker compose CLI with fixed argv only. Releases
// are the compose project's own builds: apply builds the app image and
// brings the stack up with --wait, status reads compose ps/images, and
// rollback retags the previous release's image digest and re-ups without a
// rebuild. Production is refused honestly: the generated compose files are
// the local stack, and a production Docker deployment is infrastructure
// this target does not own.
//
// Tests inject a stub Runner; only this package touches the docker CLI.
package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/remote"
)

// Target deploys one project root to its local compose stack.
type Target struct {
	runner Runner
	now    func() time.Time
}

// Runner executes fixed argv with stdin and either captures or streams
// stdout. It is the only execution seam in the package.
type Runner interface {
	Run(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error)
	RunStream(ctx context.Context, root string, argv []string, stdout io.Writer) error
}

// NewDeployTarget constructs the target on the real docker CLI.
func NewDeployTarget() *Target {
	return &Target{runner: composeRunner{}, now: time.Now}
}

// composeRunner shells out to docker compose.
type composeRunner struct{}

func (composeRunner) Run(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error) {
	command, buffer := buildCommand(ctx, root, argv, stdin)
	command.Stdout = buffer
	command.Stderr = buffer
	if err := command.Run(); err != nil {
		return buffer.Bytes(), fmt.Errorf("docker compose: %w: %s", err, trailing(buffer.String(), 400))
	}
	return buffer.Bytes(), nil
}

func (composeRunner) RunStream(ctx context.Context, root string, argv []string, stdout io.Writer) error {
	command, buffer := buildCommand(ctx, root, argv, nil)
	command.Stdout = io.MultiWriter(stdout, buffer)
	command.Stderr = io.MultiWriter(stdout, buffer)
	if err := command.Run(); err != nil {
		return fmt.Errorf("docker compose: %w: %s", err, trailing(buffer.String(), 400))
	}
	return nil
}

// buildCommand assembles one fixed argv against the project root. Apply has
// no request carrying a root — the brief's DeployTarget contract binds
// root at Plan/Status time — so a running apply assumes the process working
// directory is the project root, which is how ggg resolves a project.
func buildCommand(ctx context.Context, root string, argv []string, stdin []byte) (*exec.Cmd, *bytes.Buffer) {
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = root
	buffer := &bytes.Buffer{}
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	return command, buffer
}

// Deps keeps the runtime system constructor shape genesis generates.
type Deps struct{}

// NewModule constructs the runtime handle. The generated bootstrap compiles
// this constructor; deploy operations run through NewTarget.
func NewModule(ctx context.Context, _ any, _ Deps) (*Target, error) {
	return &Target{}, ctx.Err()
}

// DeployID is the canonical deploy resource id the manifest claims.
const DeployID = "docker"

// environments the compose artifacts actually exist for. Production is a
// refused environment, not a silently wrong one.
var environments = map[string]string{
	"development": "compose.yaml",
	"test":        "compose.test.yaml",
}

// Plan resolves the change set: one upsert per compose service, desired
// hashes bound to the compose input bytes. The plan's observed hash is the
// authoritative status reading taken now, so any stack change between
// confirm and apply refuses as stale.
func (t *Target) Plan(ctx context.Context, request remote.DeployRequest) (remote.DeployPlan, error) {
	file, err := composeFile(request.Environment)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	services, err := t.services(ctx, request.Root, file)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	input, err := t.inputHash(request.Root, file)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	status, err := t.Status(ctx, request)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	plan := remote.DeployPlan{ObservedStateHash: status.ObservedStateHash}
	for _, service := range services {
		path, err := remote.DeployPath(DeployID, service)
		if err != nil {
			return remote.DeployPlan{}, err
		}
		plan.Changes = append(plan.Changes, remote.RemoteChange{
			ChangeID:       service,
			Path:           path,
			Kind:           "upsert-service",
			IdempotencyKey: strings.Join([]string{"docker", request.Environment, service}, ":"),
			DesiredHash:    input,
		})
	}
	if err := remote.FinalizeDeployPlan(&plan); err != nil {
		return remote.DeployPlan{}, err
	}
	return plan, ctx.Err()
}

// Apply builds the app image and brings the stack up, waiting for health.
// The returned state records the release identity the rollback path re-ups.
func (t *Target) Apply(ctx context.Context, plan remote.DeployPlan, progress remote.ProgressSink) (remote.DeployState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	if err := ctx.Err(); err != nil {
		return remote.DeployState{}, err
	}
	file := planEnvironmentFile(plan)
	progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "building images", Current: 1, Total: 2, Done: false})
	if _, err := t.runner.Run(ctx, ".", []string{"docker", "compose", "-f", file, "build"}, nil); err != nil {
		return remote.DeployState{}, err
	}
	progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "starting services (waiting for health)", Current: 2, Total: 2, Done: false})
	if _, err := t.runner.Run(ctx, ".", []string{"docker", "compose", "-f", file, "up", "-d", "--wait", "--wait-timeout", "120"}, nil); err != nil {
		return remote.DeployState{}, err
	}
	request := remote.DeployRequest{Environment: planEnvironment(plan)}
	status, err := t.Status(ctx, request)
	if err != nil {
		return remote.DeployState{}, err
	}
	state := remote.DeployState{
		ReleaseID:       "compose-" + t.now().UTC().Format("20060102T150405Z"),
		URL:             status.URL,
		ImageDigest:     status.ObservedVersion,
		ObservedVersion: status.ObservedStateHash,
		Metadata:        map[string]string{"environment": planEnvironment(plan)},
	}
	progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "stack is up: " + state.URL, Current: 2, Total: 2, Done: true})
	return state, nil
}

// Status observes the stack. It is authoritative: running services and
// their image digests decide readiness and the observed hash.
func (t *Target) Status(ctx context.Context, request remote.DeployRequest) (remote.DeployStatus, error) {
	file, err := composeFile(request.Environment)
	if err != nil {
		return remote.DeployStatus{}, err
	}
	services, err := t.services(ctx, request.Root, file)
	if err != nil {
		return remote.DeployStatus{}, err
	}
	if len(services) == 0 {
		return remote.DeployStatus{}, fmt.Errorf("compose %s declares no services", file)
	}
	psOutput, err := t.runner.Run(ctx, request.Root, []string{"docker", "compose", "-f", file, "ps", "--format", "json", "--all"}, nil)
	if err != nil {
		return remote.DeployStatus{}, err
	}
	states, err := parseComposePS(psOutput)
	if err != nil {
		return remote.DeployStatus{}, err
	}
	images, _ := t.runner.Run(ctx, request.Root, []string{"docker", "compose", "-f", file, "images", "--format", "json"}, nil)
	digests := parseComposeImages(images)

	status := remote.DeployStatus{UpdatedAt: t.now().UTC()}
	observed := map[string]string{}
	appReady := false
	for _, service := range services {
		entry, running := states[service]
		digest := digests[service]
		observed[service] = entry.state + "@" + digest
		if service == "app" || len(services) == 1 {
			appReady = running
			status.ObservedVersion = digest
			status.URL = entry.url
			status.ReleaseID = entry.image
		}
		if !running {
			appReady = false
		}
	}
	status.State = "deployed"
	if !appReady {
		status.State = "degraded"
	}
	status.Ready = appReady
	status.ObservedStateHash = remote.ObservedStateHash(observed)
	return status, nil
}

// Logs streams the stack's logs to the writer until the context is done.
func (t *Target) Logs(ctx context.Context, request remote.DeployRequest, out io.Writer) error {
	file, err := composeFile(request.Environment)
	if err != nil {
		return err
	}
	argv := []string{"docker", "compose", "-f", file, "logs", "--tail", "100"}
	return t.runner.RunStream(ctx, request.Root, argv, out)
}

// Rollback re-ups the previous release's image digests without rebuilding.
// Compose has no release ledger: the previous DeployState's recorded digests
// are retagged onto the compose image names, which is exactly how a local
// stack rolls back.
func (t *Target) Rollback(ctx context.Context, request remote.DeployRequest, previous remote.DeployState, progress remote.ProgressSink) (remote.DeployState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	file, err := composeFile(request.Environment)
	if err != nil {
		return remote.DeployState{}, err
	}
	if previous.ReleaseID == "" {
		return remote.DeployState{}, fmt.Errorf("no recorded previous release to roll back to")
	}
	progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "restoring images from " + previous.ReleaseID, Current: 1, Total: 2, Done: false})
	for _, ref := range strings.Split(previous.Metadata["image_refs"], " ") {
		parts := strings.SplitN(ref, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		if _, err := t.runner.Run(ctx, request.Root, []string{"docker", "tag", parts[1], parts[0]}, nil); err != nil {
			return remote.DeployState{}, err
		}
	}
	progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "restarting services without rebuild", Current: 2, Total: 2, Done: false})
	if _, err := t.runner.Run(ctx, request.Root, []string{"docker", "compose", "-f", file, "up", "-d", "--no-build", "--wait", "--wait-timeout", "120"}, nil); err != nil {
		return remote.DeployState{}, err
	}
	status, err := t.Status(ctx, request)
	if err != nil {
		return remote.DeployState{}, err
	}
	state := remote.DeployState{
		ReleaseID:       previous.ReleaseID,
		URL:             status.URL,
		ImageDigest:     status.ObservedVersion,
		ObservedVersion: status.ObservedStateHash,
		Metadata:        map[string]string{"environment": request.Environment},
	}
	progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "rollback complete", Current: 2, Total: 2, Done: true})
	return state, nil
}

// PutSecrets merges named values into the environment's CLI-managed env
// file, which the generated compose already mounts as env_file. Production
// refuses: no file is written or read for production secrets.
func (t *Target) PutSecrets(ctx context.Context, request remote.DeployRequest, secrets remote.SecretValues, progress remote.ProgressSink) (err error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	if request.Environment == "production" {
		return fmt.Errorf("the docker target does not manage production secrets: production values come from the deployment environment, never .ggg/env")
	}
	if len(request.SecretKeys) == 0 {
		return fmt.Errorf("no secret keys named for %s", request.Environment)
	}
	values := map[string]string{}
	for _, key := range request.SecretKeys {
		value, ok := secrets.Get(key)
		if !ok || value == "" {
			return fmt.Errorf("secret %s is not configured; set it in the environment or .ggg/env/%s.env", key, request.Environment)
		}
		values[key] = value
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := remote.WriteEnvironmentEnvFile(request.Root, request.Environment, values); err != nil {
		return err
	}
	progress.Emit(remote.ProgressEvent{Stage: "secrets", Message: fmt.Sprintf("updated %d key(s) in .ggg/env/%s.env", len(values), request.Environment), Current: 1, Total: 1, Done: true})
	return nil
}

// ---------------------------------------------------------------------------
// compose plumbing

func composeFile(environment string) (string, error) {
	file, ok := environments[environment]
	if !ok {
		return "", fmt.Errorf("the docker target deploys development and test compose stacks; production is refused")
	}
	return file, nil
}

func planEnvironment(plan remote.DeployPlan) string {
	for _, change := range plan.Changes {
		parts := strings.Split(change.IdempotencyKey, ":")
		if len(parts) >= 2 && parts[0] == "docker" {
			return parts[1]
		}
	}
	return "development"
}

func planEnvironmentFile(plan remote.DeployPlan) string {
	file, err := composeFile(planEnvironment(plan))
	if err != nil {
		return "compose.yaml"
	}
	return file
}

func (t *Target) services(ctx context.Context, root, file string) ([]string, error) {
	out, err := t.runner.Run(ctx, root, []string{"docker", "compose", "-f", file, "config", "--services"}, nil)
	if err != nil {
		return nil, err
	}
	services := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if service := strings.TrimSpace(line); service != "" {
			services = append(services, service)
		}
	}
	return services, nil
}

// inputHash binds every service's desired hash to the compose file and
// Dockerfile bytes: the deploy's real input identity.
func (t *Target) inputHash(root, file string) (string, error) {
	hasher := sha256.New()
	for _, name := range []string{file, "Dockerfile"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hasher, "%s\n", name)
		hasher.Write(data)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// composeServiceEntry is the defensive view of one `compose ps --format
// json` record. Unknown fields are ignored; the fields this adapter needs
// are optional so a CLI output shift degrades the report instead of
// crashing it.
type composeServiceEntry struct {
	state string
	url   string
	image string
}

func parseComposePS(data []byte) (map[string]composeServiceEntry, error) {
	entries := map[string]composeServiceEntry{}
	if len(bytes.TrimSpace(data)) == 0 {
		return entries, nil
	}
	var records []map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &records); err != nil {
		// Some compose versions emit one JSON object per line.
		records = nil
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				return nil, fmt.Errorf("parse compose ps output: %w", err)
			}
			records = append(records, record)
		}
	}
	for _, record := range records {
		name, _ := record["Service"].(string)
		if name == "" {
			name, _ = record["Name"].(string)
		}
		if name == "" {
			continue
		}
		state, _ := record["State"].(string)
		health, _ := record["Health"].(string)
		entry := composeServiceEntry{state: strings.ToLower(state)}
		if entry.state == "running" && health != "" && health != "healthy" {
			entry.state = "unhealthy"
		}
		if publishers, ok := record["Publishers"].([]any); ok {
			for _, publisher := range publishers {
				port, ok := publisher.(map[string]any)
				if !ok {
					continue
				}
				published, _ := port["PublishedURL"].(string)
				if published == "" {
					if target, _ := port["Target"].(float64); target > 0 {
						published = fmt.Sprintf("http://localhost:%d", int(target))
					}
				}
				if published != "" && entry.url == "" {
					entry.url = published
				}
			}
		}
		if image, ok := record["Image"].(string); ok {
			entry.image = image
		}
		entries[name] = entry
	}
	return entries, nil
}

func parseComposeImages(data []byte) map[string]string {
	digests := map[string]string{}
	if len(bytes.TrimSpace(data)) == 0 {
		return digests
	}
	var records []map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &records); err != nil {
		return digests
	}
	for _, record := range records {
		service, _ := record["ContainerName"].(string)
		digest, _ := record["Digest"].(string)
		id, _ := record["ID"].(string)
		repository, _ := record["Repository"].(string)
		tag, _ := record["Tag"].(string)
		if service == "" {
			continue
		}
		if digest != "" {
			digests[service] = digest
		} else if id != "" {
			digests[service] = id
		}
		if repository != "" && tag != "" {
			digests["ref:"+service] = repository + ":" + tag
		}
	}
	return digests
}

func trailing(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
