// Package neon implements the provider provisioner for the Neon managed
// Postgres target through the official Neon v2 control-plane API.
//
// This package is the only place Neon is called. The CLI reaches it
// exclusively through the remote.ProviderProvisioner interface, and the one
// credential a provision produces — the branch connection URI — leaves
// through a remote.SecretSink, never through a plan, a state file, argv, or
// rendered output.
//
// Plan and Check resolve NEON_API_KEY through the request's SecretValues
// (the CLI's layered lookup). Apply receives no secret reader by contract,
// so it authenticates through the process environment, which is where a
// production operator's API key lives.
package neon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/remote"
)

// DefaultBaseURL is the Neon control-plane API root.
const DefaultBaseURL = "https://api.neon.tech/v2"

// APIKeyEnv names the secret the API authenticates with.
const APIKeyEnv = "NEON_API_KEY"

// baseURLEnv lets contract tests and air-gapped operators point the client
// at a compatible endpoint without code changes.
const baseURLEnv = "NEON_API_BASE_URL"

// Provisioner provisions Neon projects and branches for one target.
type Provisioner struct {
	baseURL string
	client  *http.Client
	// envLookup is the credential source Apply uses. Tests inject a stub.
	envLookup func(string) (string, bool)
}

// NewProvisioner constructs the provisioner with the public API base URL.
func NewProvisioner() *Provisioner {
	baseURL := strings.TrimRight(os.Getenv(baseURLEnv), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Provisioner{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
		envLookup: func(key string) (string, bool) {
			return os.LookupEnv(key)
		},
	}
}

// ---------------------------------------------------------------------------
// Resource naming

// resourceID is the branch resource's canonical id for one environment.
func resourceID(environment string) string { return "branch-" + environment }

// idempotencyKey names the (resource, human slug) pair so a replay
// reconciles the same target instead of duplicating it. Slugs are derived
// names, never secret values.
func idempotencyKey(request remote.ProviderRequest, resource, slug string) string {
	return strings.Join([]string{"neon", request.Environment, request.Target, resource, slug}, ":")
}

// keySlug recovers the human name an idempotency key carries.
func keySlug(change remote.RemoteChange) string {
	parts := strings.Split(change.IdempotencyKey, ":")
	if len(parts) >= 5 {
		return parts[4]
	}
	return ""
}

func (p *Provisioner) projectName(request remote.ProviderRequest) string {
	if name := request.Values["project_name"]; name != "" {
		return name
	}
	base := "gogogadget"
	if request.Root != "" {
		base = filepath.Base(request.Root)
	}
	return base + "-" + request.Environment
}

func (p *Provisioner) branchName(request remote.ProviderRequest) string {
	if name := request.Values["branch_name"]; name != "" {
		return name
	}
	return "ggg-" + request.Environment
}

func (p *Provisioner) projectID(request remote.ProviderRequest) string {
	if id := request.Values["project_id"]; id != "" {
		return id
	}
	return request.Prior.ResourceIDs["project_id"]
}

// ---------------------------------------------------------------------------
// remote.ProviderProvisioner

// Plan resolves the change set that brings the target to one project with
// one environment branch. An absent API key is the typed not-configured
// refusal: the CLI renders the checklist and console link instead of a fake
// plan.
func (p *Provisioner) Plan(ctx context.Context, request remote.ProviderRequest) (remote.ProviderPlan, error) {
	if lookup(request.Secrets, APIKeyEnv) == "" {
		return remote.ProviderPlan{}, &remote.ErrNotConfigured{
			Missing: []string{APIKeyEnv}, Console: "https://console.neon.tech",
			Advice: "create a Neon API key, run ggg provider configure, then re-run ggg provider provision",
		}
	}
	plan := remote.ProviderPlan{ObservedStateHash: request.Prior.ObservedStateHash}
	projectPath, err := remote.ProviderPath(request.Adapter, request.Target, "project")
	if err != nil {
		return remote.ProviderPlan{}, err
	}
	branchPath, err := remote.ProviderPath(request.Adapter, request.Target, resourceID(request.Environment))
	if err != nil {
		return remote.ProviderPlan{}, err
	}
	if p.projectID(request) == "" {
		plan.Changes = append(plan.Changes, remote.RemoteChange{
			ChangeID: "project", Path: projectPath, Kind: "create-project",
			IdempotencyKey: idempotencyKey(request, "project", p.projectName(request)),
			DesiredHash:    remote.ObservedStateHash(map[string]string{"name": p.projectName(request)}),
			SecretKeys:     []string{"DATABASE_URL"},
		})
	}
	plan.Changes = append(plan.Changes, remote.RemoteChange{
		ChangeID: resourceID(request.Environment), Path: branchPath, Kind: "create-branch",
		IdempotencyKey: idempotencyKey(request, resourceID(request.Environment), p.branchName(request)),
		DesiredHash:    remote.ObservedStateHash(map[string]string{"name": p.branchName(request)}),
		DependsOn:      []string{"project"},
		SecretKeys:     []string{"DATABASE_URL"},
	})
	if err := remote.FinalizePlan(&plan); err != nil {
		return remote.ProviderPlan{}, err
	}
	return plan, ctx.Err()
}

// Apply executes the confirmed changes in order. Project creation is
// reconciling: a partial apply that already created the project but died
// before the branch resumes with only the branch change pending, and the
// recorded project id is what Plan saw when it confirmed. The branch
// connection URI goes to the secret sink and nowhere else.
func (p *Provisioner) Apply(ctx context.Context, plan remote.ProviderPlan, secrets remote.SecretSink, progress remote.ProgressSink) (remote.ProviderState, error) {
	if progress == nil {
		progress = remote.DiscardProgress
	}
	if secrets == nil {
		return remote.ProviderState{}, errors.New("neon apply requires a secret sink")
	}
	key := lookup(remote.SecretValuesFunc(p.envLookup), APIKeyEnv)
	if key == "" {
		return remote.ProviderState{}, &remote.ErrNotConfigured{
			Missing: []string{APIKeyEnv}, Console: "https://console.neon.tech",
			Advice: "the apply process environment must carry NEON_API_KEY",
		}
	}
	state := remote.ProviderState{ResourceIDs: map[string]string{}}
	for _, change := range plan.Changes {
		if err := ctx.Err(); err != nil {
			return state, err
		}
		switch change.Kind {
		case "create-project":
			progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "creating Neon project " + keySlug(change), Done: false})
			project, err := p.createProject(ctx, key, keySlug(change))
			if err != nil {
				return state, err
			}
			state.ResourceIDs["project_id"] = project.ID
			progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "Neon project " + project.ID + " created", Current: 1, Total: 2, Done: false})
		case "create-branch":
			projectID := state.ResourceIDs["project_id"]
			if projectID == "" {
				return state, fmt.Errorf("plan %s orders branch before project", shortHash(plan.PlanHash))
			}
			name := keySlug(change)
			progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "creating branch " + name, Current: 2, Total: 2, Done: false})
			branch, err := p.createBranch(ctx, key, projectID, name)
			if err != nil {
				return state, err
			}
			state.ResourceIDs["branch_id"] = branch.ID
			uri, err := p.connectionURI(ctx, key, projectID, branch.ID)
			if err != nil {
				return state, err
			}
			if err := secrets.Put(ctx, "DATABASE_URL", uri); err != nil {
				return state, fmt.Errorf("store DATABASE_URL: %w", err)
			}
			progress.Emit(remote.ProgressEvent{Stage: "apply", Message: "branch ready; connection URI stored as DATABASE_URL", Current: 2, Total: 2, Done: true})
		default:
			return state, fmt.Errorf("plan carries unsupported change kind %q", change.Kind)
		}
	}
	state.ObservedStateHash = remote.ObservedStateHash(state.ResourceIDs)
	return state, nil
}

// Check observes the configured project and branch. It is the authoritative
// reading: a stored state that disagrees with it is stale.
func (p *Provisioner) Check(ctx context.Context, request remote.ProviderRequest) (remote.ProviderStatus, error) {
	key := lookup(request.Secrets, APIKeyEnv)
	if key == "" {
		return remote.ProviderStatus{}, &remote.ErrNotConfigured{
			Missing: []string{APIKeyEnv}, Console: "https://console.neon.tech",
			Advice: "create a Neon API key, then re-run ggg provider test",
		}
	}
	projectID := p.projectID(request)
	status := remote.ProviderStatus{CheckedAt: time.Now().UTC()}
	if projectID == "" {
		status.State, status.Message = "absent", "no Neon project recorded for this target"
		return status, nil
	}
	project, err := p.getProject(ctx, key, projectID)
	if err != nil {
		return remote.ProviderStatus{}, err
	}
	observed := map[string]string{"project_id": project.ID}
	message := "project " + project.ID
	healthy := true
	if branchID := request.Prior.ResourceIDs["branch_id"]; branchID != "" {
		branch, err := p.getBranch(ctx, key, projectID, branchID)
		if err != nil {
			return remote.ProviderStatus{}, err
		}
		observed["branch_id"] = branch.ID
		message += ", branch " + branch.ID
		if branch.Archived {
			healthy = false
			observed["archived"] = "true"
			message += " (archived)"
		}
	} else {
		message += ", no branch provisioned"
	}
	status.State = "ready"
	status.Message = message
	status.Healthy = healthy
	status.ObservedStateHash = remote.ObservedStateHash(observed)
	return status, nil
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// ---------------------------------------------------------------------------
// Neon v2 API client

type apiProject struct {
	ID string `json:"id"`
}

type apiBranch struct {
	ID       string `json:"id"`
	Archived bool   `json:"archived"`
}

func (p *Provisioner) do(ctx context.Context, method, path, key string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("neon api %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("neon api %s %s: status %d: %s", method, path, response.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

func (p *Provisioner) createProject(ctx context.Context, key, name string) (apiProject, error) {
	var wrapped struct {
		Project apiProject `json:"project"`
	}
	body := map[string]any{"project": map[string]any{"name": name}}
	if err := p.do(ctx, http.MethodPost, "/projects", key, body, &wrapped); err != nil {
		return apiProject{}, err
	}
	if wrapped.Project.ID == "" {
		return apiProject{}, errors.New("neon api returned no project id")
	}
	return wrapped.Project, nil
}

func (p *Provisioner) createBranch(ctx context.Context, key, projectID, name string) (apiBranch, error) {
	var wrapped struct {
		Branch apiBranch `json:"branch"`
	}
	body := map[string]any{"branch": map[string]any{"name": name}}
	if err := p.do(ctx, http.MethodPost, "/projects/"+projectID+"/branches", key, body, &wrapped); err != nil {
		return apiBranch{}, err
	}
	if wrapped.Branch.ID == "" {
		return apiBranch{}, errors.New("neon api returned no branch id")
	}
	return wrapped.Branch, nil
}

func (p *Provisioner) connectionURI(ctx context.Context, key, projectID, branchID string) (string, error) {
	var wrapped struct {
		URI string `json:"uri"`
	}
	path := fmt.Sprintf("/projects/%s/connection_uri?branch_id=%s&database=neondb&role=neondb_owner&pooler=true", projectID, branchID)
	if err := p.do(ctx, http.MethodGet, path, key, nil, &wrapped); err != nil {
		return "", err
	}
	if wrapped.URI == "" {
		return "", errors.New("neon api returned an empty connection uri")
	}
	return wrapped.URI, nil
}

func (p *Provisioner) getProject(ctx context.Context, key, projectID string) (apiProject, error) {
	var wrapped struct {
		Project apiProject `json:"project"`
	}
	if err := p.do(ctx, http.MethodGet, "/projects/"+projectID, key, nil, &wrapped); err != nil {
		return apiProject{}, err
	}
	return wrapped.Project, nil
}

func (p *Provisioner) getBranch(ctx context.Context, key, projectID, branchID string) (apiBranch, error) {
	var wrapped struct {
		Branch apiBranch `json:"branch"`
	}
	if err := p.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%s/branches/%s", projectID, branchID), key, nil, &wrapped); err != nil {
		return apiBranch{}, err
	}
	return wrapped.Branch, nil
}

func lookup(secrets remote.SecretValues, key string) string {
	if secrets == nil {
		return ""
	}
	value, _ := secrets.Get(key)
	return value
}
