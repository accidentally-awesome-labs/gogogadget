// Package remote defines the typed contracts every provider provisioner,
// deployment target, and database operator implements, plus the small
// vocabulary — progress events, secret handles, remote change paths — the ggg
// CLI and the adapter packages share.
//
// The contracts are deliberately narrow. Provisioners and deployers observe
// and mutate provider-side state through their own SDKs; the CLI never sees
// those clients. Plans carry resource identities and hashes, never secret
// values: SecretKeys name what an apply will consume, SecretSink is the only
// channel a produced credential may leave through, and SecretValues is the
// only channel existing credentials arrive through.
package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// ProgressEvent reports one step of a provision, deploy, backup, or restore.
// Stage names the phase ("plan", "apply", "verify"), Message is human text,
// and Current/Total bound the phase when known. Done marks the final event.
type ProgressEvent struct {
	Stage   string
	Message string
	Current int
	Total   int
	Done    bool
}

// ProgressSink consumes progress events. Human and TUI front ends render
// them; JSON runs pass a discard sink because the envelope is the output.
type ProgressSink interface {
	Emit(ProgressEvent)
}

// ProgressFunc adapts a function to ProgressSink.
type ProgressFunc func(ProgressEvent)

// Emit implements ProgressSink.
func (f ProgressFunc) Emit(event ProgressEvent) { f(event) }

// DiscardProgress consumes events without rendering them.
var DiscardProgress ProgressSink = ProgressFunc(func(ProgressEvent) {})

// SecretValues resolves named secret values. Only provisioners, deployers,
// and database operators read from it; command handlers never hold one.
type SecretValues interface {
	Get(key string) (string, bool)
}

// SecretValuesFunc adapts a lookup function to SecretValues.
type SecretValuesFunc func(key string) (string, bool)

// Get implements SecretValues.
func (f SecretValuesFunc) Get(key string) (string, bool) { return f(key) }

// EmptySecrets resolves nothing: the value every non-secret context uses.
var EmptySecrets SecretValues = SecretValuesFunc(func(string) (string, bool) { return "", false })

// SecretSink receives secret values produced or fetched during an apply —
// a provisioner's connection URI, a deployer's fetched credential. Values
// flow into the target's own store, never into plans, state dumps, argv,
// or rendered output.
type SecretSink interface {
	Put(ctx context.Context, key, value string) error
}

// ---------------------------------------------------------------------------
// Remote change paths

// Path grammars for remote changes. Values never appear in a path: a path
// names one canonical resource identity, nothing else.
//
//	provider://<adapter>@<target>/<resource-id>
//	deploy://<deploy-id>/<resource-id>
const (
	ProviderPathScheme = "provider://"
	DeployPathScheme   = "deploy://"
)

// ProviderPath renders the canonical path of one provider resource.
func ProviderPath(adapter, target, resourceID string) (string, error) {
	if adapter == "" || target == "" || resourceID == "" {
		return "", fmt.Errorf("provider path requires adapter, target, and resource id")
	}
	if strings.ContainsAny(adapter+"/", " \t\n") || strings.ContainsAny(target+resourceID, " \t\n") {
		return "", fmt.Errorf("provider path %s@%s/%s contains whitespace", adapter, target, resourceID)
	}
	return fmt.Sprintf("%s%s@%s/%s", ProviderPathScheme, adapter, target, resourceID), nil
}

// DeployPath renders the canonical path of one deployment resource.
func DeployPath(deployID, resourceID string) (string, error) {
	if deployID == "" || resourceID == "" {
		return "", fmt.Errorf("deploy path requires deploy id and resource id")
	}
	if strings.ContainsAny(deployID+resourceID, " \t\n") {
		return "", fmt.Errorf("deploy path %s/%s contains whitespace", deployID, resourceID)
	}
	return fmt.Sprintf("%s%s/%s", DeployPathScheme, deployID, resourceID), nil
}

// ValidateRemotePath accepts only the two documented path grammars.
func ValidateRemotePath(path string) error {
	if path == "" {
		return fmt.Errorf("remote path is empty")
	}
	switch {
	case strings.HasPrefix(path, ProviderPathScheme):
		rest := strings.TrimPrefix(path, ProviderPathScheme)
		adapterTarget, resourceID, ok := strings.Cut(rest, "/")
		if !ok || adapterTarget == "" || resourceID == "" || strings.Contains(resourceID, "/") {
			return fmt.Errorf("remote path %q is not provider://<adapter>@<target>/<resource-id>", path)
		}
		adapter, target, ok := strings.Cut(adapterTarget, "@")
		if !ok || adapter == "" || target == "" || strings.Contains(target, "@") {
			return fmt.Errorf("remote path %q is not provider://<adapter>@<target>/<resource-id>", path)
		}
		return nil
	case strings.HasPrefix(path, DeployPathScheme):
		rest := strings.TrimPrefix(path, DeployPathScheme)
		deployID, resourceID, ok := strings.Cut(rest, "/")
		if !ok || deployID == "" || resourceID == "" || strings.Contains(resourceID, "/") || strings.Contains(deployID, "/") {
			return fmt.Errorf("remote path %q is not deploy://<deploy-id>/<resource-id>", path)
		}
		return nil
	default:
		return fmt.Errorf("remote path %q must start with %s or %s", path, ProviderPathScheme, DeployPathScheme)
	}
}

// RemoteChange is one provider-side or deployment-side change. It names the
// canonical resource, the operation kind, the idempotency key an apply may
// replay under, and the hashes an operator confirms — never values, and
// never the secret values an apply consumes.
type RemoteChange struct {
	ChangeID        string   `json:"change_id"`
	Path            string   `json:"path"`
	Kind            string   `json:"kind"`
	IdempotencyKey  string   `json:"idempotency_key"`
	DesiredHash     string   `json:"desired_hash"`
	ObservedVersion string   `json:"observed_version"`
	DependsOn       []string `json:"depends_on"`
	SecretKeys      []string `json:"secret_keys"`
}

// ---------------------------------------------------------------------------
// Provider provisioning

// ProviderRequest carries one provision operation's identity and inputs.
// Values are non-secret target inputs; Secrets resolves declared secret keys.
type ProviderRequest struct {
	Root        string
	Slot        string
	Environment string
	Adapter     string
	Target      string
	Values      map[string]string
	Secrets     SecretValues
	Prior       ProviderState
}

// ProviderState is the persisted result of one provision apply.
type ProviderState struct {
	ResourceIDs       map[string]string
	ObservedStateHash string
}

// ProviderStatus is the authoritative observation of one provider resource
// set. Check is the only source an operator trusts over a stored state.
type ProviderStatus struct {
	State             string
	Message           string
	ObservedStateHash string
	Healthy           bool
	CheckedAt         time.Time
}

// ProviderPlan is the ordered, confirmable change set one provision apply
// executes. PlanHash binds the confirmed plan to the apply that runs it.
type ProviderPlan struct {
	PlanHash          string
	ObservedStateHash string
	Changes           []RemoteChange
}

// ProviderProvisioner plans, applies, and observes one adapter target's
// provider-side resources.
type ProviderProvisioner interface {
	Plan(ctx context.Context, request ProviderRequest) (ProviderPlan, error)
	Apply(ctx context.Context, plan ProviderPlan, secrets SecretSink, progress ProgressSink) (ProviderState, error)
	Check(ctx context.Context, request ProviderRequest) (ProviderStatus, error)
}

// ---------------------------------------------------------------------------
// Deployment

// DeployRequest carries one deployment operation's identity. Secrets are not
// part of a deploy request: PutSecrets receives them explicitly, and every
// other method works from identifiers alone.
type DeployRequest struct {
	Root        string
	Environment string
	Target      string
	State       DeployState
	SecretKeys  []string
}

// DeployState is the persisted result of one deploy apply.
type DeployState struct {
	ReleaseID       string
	URL             string
	ImageDigest     string
	ObservedVersion string
	Metadata        map[string]string
}

// DeployStatus is the authoritative observation of one deployment.
type DeployStatus struct {
	State             string
	ReleaseID         string
	URL               string
	ObservedVersion   string
	ObservedStateHash string
	Ready             bool
	UpdatedAt         time.Time
}

// DeployPlan is the ordered, confirmable change set one deploy apply executes.
type DeployPlan struct {
	PlanHash          string
	ObservedStateHash string
	Changes           []RemoteChange
}

// DeployTarget plans, applies, observes, and rolls back one deployment
// integration.
type DeployTarget interface {
	Plan(ctx context.Context, request DeployRequest) (DeployPlan, error)
	Apply(ctx context.Context, plan DeployPlan, progress ProgressSink) (DeployState, error)
	Status(ctx context.Context, request DeployRequest) (DeployStatus, error)
	Logs(ctx context.Context, request DeployRequest, out io.Writer) error
	Rollback(ctx context.Context, request DeployRequest, previous DeployState, progress ProgressSink) (DeployState, error)
	PutSecrets(ctx context.Context, request DeployRequest, secrets SecretValues, progress ProgressSink) error
}

// ---------------------------------------------------------------------------
// Database operations

// DatabaseRequest carries one backup/restore operation's identity. Secret
// values arrive through Secrets; State carries adapter-resolved facts such
// as the compose service name.
type DatabaseRequest struct {
	Root        string
	Environment string
	Adapter     string
	Target      string
	Secrets     SecretValues
	State       map[string]string
}

// BackupState describes one completed backup artifact.
type BackupState struct {
	ID        string
	Location  string
	SHA256    string
	CreatedAt time.Time
}

// RestoreState describes one completed restore into a new database. URLKey
// names the environment key whose value would address the restored database;
// the URL itself is never rendered.
type RestoreState struct {
	DatabaseID string
	URLKey     string
	Ready      bool
}

// DrillResult describes one completed restore drill.
type DrillResult struct {
	BackupID    string
	DatabaseID  string
	Ready       bool
	SmokePassed bool
	Duration    time.Duration
}

// DatabaseOperator backs up, restores, and drills one database target.
// Restore always creates a new database and verifies it; it never overwrites
// the active one.
type DatabaseOperator interface {
	Backup(ctx context.Context, request DatabaseRequest, destination string, progress ProgressSink) (BackupState, error)
	Restore(ctx context.Context, request DatabaseRequest, backup BackupState, destinationURLKey string, secrets SecretValues, progress ProgressSink) (RestoreState, error)
	RestoreDrill(ctx context.Context, request DatabaseRequest, backup BackupState, secrets SecretValues, progress ProgressSink) (DrillResult, error)
}

// ---------------------------------------------------------------------------
// Plan hashing

// PlanHash derives the deterministic hash that binds a confirmed plan to the
// apply that executes it: any change to the ordered change set, the observed
// hash, or the kinds changes the hash and refuses the apply.
func PlanHash(observedStateHash string, changes []RemoteChange) string {
	hasher := sha256.New()
	fmt.Fprintf(hasher, "observed:%s\n", observedStateHash)
	ordered := append([]RemoteChange{}, changes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ChangeID < ordered[j].ChangeID })
	for _, change := range ordered {
		fmt.Fprintf(hasher, "change:%s\npath:%s\nkind:%s\nidempotency:%s\ndesired:%s\n",
			change.ChangeID, change.Path, change.Kind, change.IdempotencyKey, change.DesiredHash)
		for _, dep := range change.DependsOn {
			fmt.Fprintf(hasher, "depends:%s\n", dep)
		}
		for _, key := range change.SecretKeys {
			fmt.Fprintf(hasher, "secret:%s\n", key)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// FinalizePlan fills a plan's PlanHash from its own content and validates
// every change path, so an adapter cannot emit a plan the CLI would refuse.
func FinalizePlan(plan *ProviderPlan) error {
	for _, change := range plan.Changes {
		if err := ValidateRemotePath(change.Path); err != nil {
			return err
		}
	}
	plan.PlanHash = PlanHash(plan.ObservedStateHash, plan.Changes)
	return nil
}

// FinalizeDeployPlan fills a deploy plan's PlanHash from its own content and
// validates every change path.
func FinalizeDeployPlan(plan *DeployPlan) error {
	for _, change := range plan.Changes {
		if err := ValidateRemotePath(change.Path); err != nil {
			return err
		}
	}
	plan.PlanHash = PlanHash(plan.ObservedStateHash, plan.Changes)
	return nil
}

// ObservedStateHash derives the state hash a Check or Status reports, from
// any canonical description of the observed resources. Providers and the CLI
// compute it the same way, so a changed target refuses a stale plan.
func ObservedStateHash(description any) string {
	encoded, err := json.Marshal(description)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// ErrNotConfigured is the typed refusal a provisioner or deployer returns
// when its API credential or CLI tool is missing. Message names every key
// or binary the operator must supply; the CLI renders it as a checklist
// beside the target's console URL instead of pretending to provision.
type ErrNotConfigured struct {
	Missing []string
	Console string
	Advice  string
}

func (e *ErrNotConfigured) Error() string {
	if len(e.Missing) == 0 {
		return e.Advice
	}
	return fmt.Sprintf("not configured: missing %s", strings.Join(e.Missing, ", "))
}
