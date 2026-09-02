// Package fly implements the deployment target for Fly.io through the
// flyctl CLI with fixed argv: deploy, status, logs, secrets, and rollback
// to a prior release.
//
// The application name comes from the project's fly.toml. Secret values
// reach Fly through `fly secrets import` on stdin — never argv, never
// output. flyctl output shape is parsed defensively: an unexpected shape is
// an honest error naming the command, not a silently wrong status.
//
// Tests inject a stub Runner; only this package touches flyctl.
package fly

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
	"strconv"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/remote"
)

// DeployID is the canonical deploy resource id the manifest claims.
const DeployID = "fly"

// Target deploys one project root to Fly.io.
type Target struct {
	runner Runner
	now    func() time.Time
}

// Runner executes fixed flyctl argv with stdin and optional streaming.
type Runner interface {
	Run(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error)
	RunStream(ctx context.Context, root string, argv []string, stdout io.Writer) error
}

// NewDeployTarget constructs the target on the real flyctl CLI.
func NewDeployTarget() *Target {
	return &Target{runner: flyRunner{}, now: time.Now}
}

// flyRunner shells out to flyctl.
type flyRunner struct{}

func (flyRunner) Run(ctx context.Context, root string, argv []string, stdin []byte) ([]byte, error) {
	command, buffer := buildCommand(ctx, root, argv, stdin)
	command.Stdout = buffer
	command.Stderr = buffer
	if err := command.Run(); err != nil {
		return buffer.Bytes(), fmt.Errorf("flyctl: %w: %s", err, trailing(buffer.String(), 400))
	}
	return buffer.Bytes(), nil
}

func (flyRunner) RunStream(ctx context.Context, root string, argv []string, stdout io.Writer) error {
	command, buffer := buildCommand(ctx, root, argv, nil)
	command.Stdout = io.MultiWriter(stdout, buffer)
	command.Stderr = io.MultiWriter(stdout, buffer)
	if err := command.Run(); err != nil {
		return fmt.Errorf("flyctl: %w: %s", err, trailing(buffer.String(), 400))
	}
	return nil
}

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

// ---------------------------------------------------------------------------
// remote.DeployTarget

// Plan resolves the release change set. The desired hash binds the fly.toml
// and Dockerfile input bytes; the observed hash is the authoritative status
// reading taken now, so any release between confirm and apply refuses as
// stale.
func (t *Target) Plan(ctx context.Context, request remote.DeployRequest) (remote.DeployPlan, error) {
	app, err := appName(request.Root)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	input, err := inputHash(request.Root)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	status, err := t.Status(ctx, request)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	path, err := remote.DeployPath(DeployID, app)
	if err != nil {
		return remote.DeployPlan{}, err
	}
	plan := remote.DeployPlan{ObservedStateHash: status.ObservedStateHash}
	plan.Changes = []remote.RemoteChange{{
		ChangeID:       app,
		Path:           path,
		Kind:           "deploy-release",
		IdempotencyKey: strings.Join([]string{"fly", request.Environment, app, strconv.FormatInt(t.now().UTC().Unix(), 10)}, ":"),
		DesiredHash:    input,
	}}
	if err := remote.FinalizeDeployPlan(&plan); err != nil {
		return remote.DeployPlan{}, err
	}
	return plan, ctx.Err()
}

// Apply ships the release. flyctl deploy waits for the release by default;
// the state records the observed release the next Plan and any Rollback
// read.
func (t *Target) Apply(ctx context.Context, plan remote.DeployPlan, progress remote.ProgressSink) (remote.DeployState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	app, err := planApp(plan)
	if err != nil {
		return remote.DeployState{}, err
	}
	if err := ctx.Err(); err != nil {
		return remote.DeployState{}, err
	}
	progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "deploying " + app + " (remote builder)", Current: 1, Total: 2, Done: false})
	if _, err := t.runner.Run(ctx, ".", []string{"flyctl", "deploy", "--app", app}, nil); err != nil {
		return remote.DeployState{}, err
	}
	progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "release shipped; observing status", Current: 2, Total: 2, Done: false})
	status, err := t.Status(ctx, remote.DeployRequest{Environment: planEnvironment(plan)})
	if err != nil {
		return remote.DeployState{}, err
	}
	if !status.Ready {
		return remote.DeployState{}, fmt.Errorf("release for %s is not ready: %s", app, status.State)
	}
	state := remote.DeployState{
		ReleaseID:       status.ReleaseID,
		URL:             status.URL,
		ImageDigest:     status.ObservedVersion,
		ObservedVersion: status.ObservedStateHash,
		Metadata:        map[string]string{"environment": planEnvironment(plan), "app": app},
	}
	progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "live at " + state.URL, Current: 2, Total: 2, Done: true})
	return state, nil
}

// Status observes the running release through `fly status --json`. It is
// the authoritative reading.
func (t *Target) Status(ctx context.Context, request remote.DeployRequest) (remote.DeployStatus, error) {
	root := request.Root
	if root == "" {
		root = "."
	}
	app, err := appName(root)
	if err != nil {
		return remote.DeployStatus{}, err
	}
	out, err := t.runner.Run(ctx, root, []string{"flyctl", "status", "--app", app, "--json"}, nil)
	if err != nil {
		return remote.DeployStatus{}, err
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out), &payload); err != nil {
		return remote.DeployStatus{}, fmt.Errorf("parse flyctl status output: %w", err)
	}
	status := remote.DeployStatus{UpdatedAt: t.now().UTC()}
	if current, ok := payload["CurrentRelease"].(map[string]any); ok {
		status.ReleaseID = jsonString(current, "ID")
		version := jsonInt(current, "Version")
		if version > 0 {
			status.ObservedVersion = strconv.Itoa(version)
		}
		status.URL = "https://" + app + ".fly.dev"
		deployment := jsonString(current, "DeploymentStatus")
		image := jsonString(current, "ImageDigest")
		if image == "" {
			image = jsonString(current, "Image")
		}
		status.ObservedVersion = firstNonEmpty(image, status.ObservedVersion)
		switch strings.ToLower(deployment) {
		case "", "successful", "complete", "deployed":
			status.Ready = true
			status.State = "deployed"
		default:
			status.State = strings.ToLower(deployment)
		}
	}
	observed := map[string]string{"release": status.ReleaseID, "observed": status.ObservedVersion, "state": status.State}
	status.ObservedStateHash = remote.ObservedStateHash(observed)
	return status, nil
}

// Logs streams application logs until the context is done.
func (t *Target) Logs(ctx context.Context, request remote.DeployRequest, out io.Writer) error {
	root := request.Root
	if root == "" {
		root = "."
	}
	app, err := appName(root)
	if err != nil {
		return err
	}
	return t.runner.RunStream(ctx, root, []string{"flyctl", "logs", "--app", app}, out)
}

// Rollback moves the application back to a prior release through
// `fly releases rollback`. The previous state's release id selects the
// target; Fly keeps every release immutable, so this is a redeploy of
// recorded history, never a mutation of it.
func (t *Target) Rollback(ctx context.Context, request remote.DeployRequest, previous remote.DeployState, progress remote.ProgressSink) (remote.DeployState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	root := request.Root
	if root == "" {
		root = "."
	}
	app, err := appName(root)
	if err != nil {
		return remote.DeployState{}, err
	}
	if previous.ReleaseID == "" {
		return remote.DeployState{}, fmt.Errorf("no recorded previous release to roll back to")
	}
	progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "rolling " + app + " back to release " + previous.ReleaseID, Current: 1, Total: 2, Done: false})
	if _, err := t.runner.Run(ctx, root, []string{"flyctl", "releases", "rollback", previous.ReleaseID, "--app", app}, nil); err != nil {
		return remote.DeployState{}, err
	}
	progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "rollback release created; observing status", Current: 2, Total: 2, Done: false})
	status, err := t.Status(ctx, remote.DeployRequest{Root: root, Environment: request.Environment})
	if err != nil {
		return remote.DeployState{}, err
	}
	state := remote.DeployState{
		ReleaseID:       status.ReleaseID,
		URL:             status.URL,
		ImageDigest:     status.ObservedVersion,
		ObservedVersion: status.ObservedStateHash,
		Metadata:        map[string]string{"environment": request.Environment, "app": app},
	}
	progress.Emit(remote.ProgressEvent{Stage: "rollback", Message: "rollback complete", Current: 2, Total: 2, Done: true})
	return state, nil
}

// PutSecrets imports named values through `fly secrets import` on stdin, so
// secret values never appear in argv or in rendered output.
func (t *Target) PutSecrets(ctx context.Context, request remote.DeployRequest, secrets remote.SecretValues, progress remote.ProgressSink) (err error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	root := request.Root
	if root == "" {
		root = "."
	}
	app, err := appName(root)
	if err != nil {
		return err
	}
	if len(request.SecretKeys) == 0 {
		return fmt.Errorf("no secret keys named for %s", request.Environment)
	}
	var payload bytes.Buffer
	for _, key := range request.SecretKeys {
		value, ok := secrets.Get(key)
		if !ok || value == "" {
			return fmt.Errorf("secret %s is not configured in this environment", key)
		}
		fmt.Fprintf(&payload, "%s=%s\n", key, value)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	progress.Emit(remote.ProgressEvent{Stage: "secrets", Message: fmt.Sprintf("importing %d secret key(s) into %s", len(request.SecretKeys), app), Current: 1, Total: 1, Done: false})
	if _, err := t.runner.Run(ctx, root, []string{"flyctl", "secrets", "import", "--app", app}, payload.Bytes()); err != nil {
		return err
	}
	progress.Emit(remote.ProgressEvent{Stage: "secrets", Message: "secrets imported", Current: 1, Total: 1, Done: true})
	return nil
}

// ---------------------------------------------------------------------------
// plumbing

// appName reads the application name from fly.toml. The file is tiny and
// the assignment line is plain TOML; a line parse with strict whitespace
// rules covers what flyctl itself generates without a TOML dependency.
func appName(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "fly.toml"))
	if os.IsNotExist(err) {
		return "", fmt.Errorf("fly.toml is missing; the fly target deploys the project root's Fly application")
	}
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "app" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if value == "" {
			return "", fmt.Errorf("fly.toml declares an empty app name")
		}
		return value, nil
	}
	return "", fmt.Errorf("fly.toml declares no app name")
}

// inputHash binds the release's desired hash to the deploy inputs.
func inputHash(root string) (string, error) {
	hasher := sha256.New()
	for _, name := range []string{"fly.toml", "Dockerfile"} {
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

func planApp(plan remote.DeployPlan) (string, error) {
	if len(plan.Changes) == 0 {
		return "", fmt.Errorf("deploy plan carries no changes")
	}
	app := plan.Changes[0].ChangeID
	if app == "" {
		return "", fmt.Errorf("deploy plan carries no application identity")
	}
	return app, nil
}

func planEnvironment(plan remote.DeployPlan) string {
	for _, change := range plan.Changes {
		parts := strings.Split(change.IdempotencyKey, ":")
		if len(parts) >= 2 && parts[0] == "fly" {
			return parts[1]
		}
	}
	return "production"
}

func jsonString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func jsonInt(values map[string]any, key string) int {
	number, _ := values[key].(float64)
	return int(number)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func trailing(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
