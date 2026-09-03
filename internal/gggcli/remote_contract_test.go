package gggcli

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/gogogadget/gogogadget/internal/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeployTarget is a scripted deploy target. Plan and Status report
// independent observed hashes so the stale-plan refusal can be exercised.
type fakeDeployTarget struct {
	planHash     string
	planObserved string
	planChanges  []remote.RemoteChange
	planErr      error

	statusObserved string
	statusState    string
	statusReady    bool

	applied int
}

func (f *fakeDeployTarget) Plan(context.Context, remote.DeployRequest) (remote.DeployPlan, error) {
	if f.planErr != nil {
		return remote.DeployPlan{}, f.planErr
	}
	return remote.DeployPlan{PlanHash: f.planHash, ObservedStateHash: f.planObserved, Changes: f.planChanges}, nil
}

func (f *fakeDeployTarget) Apply(context.Context, remote.DeployPlan, remote.ProgressSink) (remote.DeployState, error) {
	f.applied++
	return remote.DeployState{ReleaseID: "rel-9", URL: "https://app.example.test", ObservedVersion: "v9"}, nil
}

func (f *fakeDeployTarget) Status(context.Context, remote.DeployRequest) (remote.DeployStatus, error) {
	observed := f.statusObserved
	if observed == "" {
		observed = f.planObserved
	}
	state := f.statusState
	if state == "" {
		state = "running"
	}
	return remote.DeployStatus{
		State: state, ReleaseID: "rel-8", URL: "https://app.example.test",
		ObservedVersion: "v8", ObservedStateHash: observed, Ready: f.statusReady,
		UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}, nil
}

func (f *fakeDeployTarget) Logs(context.Context, remote.DeployRequest, io.Writer) error { return nil }

func (f *fakeDeployTarget) Rollback(context.Context, remote.DeployRequest, remote.DeployState, remote.ProgressSink) (remote.DeployState, error) {
	return remote.DeployState{ReleaseID: "rel-7", ObservedVersion: "v7"}, nil
}

func (f *fakeDeployTarget) PutSecrets(context.Context, remote.DeployRequest, remote.SecretValues, remote.ProgressSink) error {
	return nil
}

// deployFixtureChanges is one ordered change set in the declared remote path
// grammar. Values never appear — the path names the canonical resource.
func deployFixtureChanges() []remote.RemoteChange {
	return []remote.RemoteChange{
		{ChangeID: "c1", Path: "deploy://fake/app", Kind: "deploy_release", IdempotencyKey: "k1", DesiredHash: "d1", ObservedVersion: "v8"},
		{ChangeID: "c2", Path: "deploy://fake/secrets", Kind: "upsert_secrets", IdempotencyKey: "k2", DesiredHash: "d2", SecretKeys: []string{"DATABASE_URL"}},
	}
}

// remoteFixture writes a project whose deployment and one provider slot are
// committed, and returns the root plus an App wired to the supplied target.
func remoteFixture(t *testing.T, target remote.DeployTarget) (string, App) {
	t.Helper()
	deployModule := baseModule("ggg/system/deploy-fake", "system", "deploy-fake")
	deployModule.Files = []modkit.ManifestFile{}
	deployModule.Runtime.Deploy = []modkit.DeployContribution{
		{ID: "fake", Package: "internal/deploy/fake", Constructor: "NewDeployTarget"},
	}
	adapter := baseModule("ggg/system/mail-dev", "system", "mail-dev")
	adapter.Files = []modkit.ManifestFile{}
	adapter.Environment = []modkit.EnvironmentVariable{{
		Key: "MAIL_DEV_DIR", Field: "MailDevDir", Type: modkit.EnvString,
		Description: "Dev mail output directory.", Targets: []string{"ggg/system/mail-dev@filesystem"},
	}}
	adapter.Runtime.System = &modkit.SystemContribution{
		Package: "internal/mail/dev", Constructor: "NewModule",
		Needs: []modkit.RuntimeNeed{}, Provides: []modkit.RuntimeProvide{},
		Adapter: &modkit.AdapterContribution{Slot: "ggg/mail", Targets: []modkit.ServiceTarget{{
			ID: "filesystem", Title: "Filesystem", Mode: "development",
			Environments: []string{"development", "test"}, Automation: "manual",
			DocsURL: "https://example.test/docs",
			Inputs: []modkit.TargetInput{
				{Key: "dir", EnvKey: "MAIL_DEV_DIR", Label: "Output directory", Type: "string", Required: false},
			},
		}}},
	}

	selections := modkit.ProviderSelections{
		Development: modkit.ProviderSelection{Adapter: adapter.ID, Target: "filesystem"},
		Test:        modkit.ProviderSelection{Adapter: adapter.ID, Target: "filesystem"},
		Production:  modkit.ProviderSelection{Adapter: adapter.ID, Target: "filesystem"},
	}
	lock := modkit.Lock{
		Schema: 2, RegistryCommit: testCommitA,
		Order:     []string{adapter.ID, deployModule.ID},
		Providers: map[string]modkit.ProviderSelections{"ggg/mail": selections},
		Modules: []modkit.LockedModule{
			{
				ID: adapter.ID, Revision: 1, Contract: 1, SourceCommit: testCommitA,
				Reason: "provider", RequiredBy: []string{}, Manifest: adapter,
				Files: []modkit.LockedFile{}, Migrations: []modkit.LockedMigration{},
			},
			{
				ID: deployModule.ID, Revision: 1, Contract: 1, SourceCommit: testCommitA,
				Reason: "deployment", RequiredBy: []string{}, Manifest: deployModule,
				Files: []modkit.LockedFile{}, Migrations: []modkit.LockedMigration{},
			},
		},
	}
	lockData, err := modkit.MarshalLock(lock)
	require.NoError(t, err)
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: testKeyA}},
		Modules:    []string{adapter.ID, deployModule.ID}, Exclude: []string{},
		Providers:  map[string]modkit.ProviderSelections{"ggg/mail": selections},
		Deployment: deployModule.ID,
	})
	require.NoError(t, err)

	root := t.TempDir()
	writeTestFile(t, root, modkit.LockFileName, lockData)
	writeTestFile(t, root, modkit.ProjectFileName, intent)
	app := App{Root: root, Version: "v1.2.3", Remote: RemoteRegistries{
		DeployTarget: func(id string) (remote.DeployTarget, bool) {
			return target, id == "fake"
		},
	}}
	return root, app
}

// TestDeployApplyAcceptsYesNoninteractively holds the remote-mutation
// contract: every remote mutation confirms, a noninteractive run confirms
// with --yes, and only its absence refuses. runAppWith drives a non-TTY
// invocation, which is exactly the CI shape the refusal used to block.
func TestDeployApplyAcceptsYesNoninteractively(t *testing.T) {
	t.Run("--yes applies", func(t *testing.T) {
		target := &fakeDeployTarget{planHash: "aaaabbbbccccdddd0000", planObserved: "obs-1", planChanges: deployFixtureChanges()}
		_, app := remoteFixture(t, target)

		out, _, err := runAppWith(t, app, "deploy", "apply", "--environment", "production", "--yes")

		require.NoError(t, err)
		assert.Equal(t, 1, target.applied, "--yes must satisfy the confirmation and reach Apply")
		assert.NotEmpty(t, out)
	})

	t.Run("--yes applies under --json", func(t *testing.T) {
		target := &fakeDeployTarget{planHash: "aaaabbbbccccdddd0000", planObserved: "obs-1", planChanges: deployFixtureChanges()}
		_, app := remoteFixture(t, target)

		out, _, err := runAppWith(t, app, "deploy", "apply", "--environment", "production", "--yes", "--json")

		require.NoError(t, err)
		assert.Equal(t, 1, target.applied)
		var envelope map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &envelope))
		assert.Equal(t, "deploy apply", envelope["command"])
	})

	for _, args := range [][]string{
		{"deploy", "apply", "--environment", "production"},
		{"deploy", "apply", "--environment", "production", "--json"},
	} {
		t.Run("refuses without --yes", func(t *testing.T) {
			target := &fakeDeployTarget{planHash: "aaaabbbbccccdddd0000", planObserved: "obs-1", planChanges: deployFixtureChanges()}
			_, app := remoteFixture(t, target)

			_, _, err := runAppWith(t, app, args...)

			require.Error(t, err)
			assert.Equal(t, 3, exitOf(t, err))
			assert.Zero(t, target.applied, "a refused confirmation must never reach Apply")
		})
	}
}

// TestDeployPlanComputesChangeSetAndIsNotStatus holds the remote-plan
// contract: `plan` calls DeployTarget.Plan and reports the ordered change set
// with its plan and observed hashes, while `status` stays the observation.
// The two envelopes must differ.
func TestDeployPlanComputesChangeSetAndIsNotStatus(t *testing.T) {
	target := &fakeDeployTarget{
		planHash: "aaaabbbbccccdddd0000", planObserved: "obs-1", planChanges: deployFixtureChanges(),
		statusState: "running", statusReady: true,
	}
	_, app := remoteFixture(t, target)

	planOut, _, err := runAppWith(t, app, "deploy", "plan", "--environment", "production", "--json")
	require.NoError(t, err)
	statusOut, _, err := runAppWith(t, app, "deploy", "status", "--environment", "production", "--json")
	require.NoError(t, err)

	var plan, status map[string]any
	require.NoError(t, json.Unmarshal([]byte(planOut), &plan))
	require.NoError(t, json.Unmarshal([]byte(statusOut), &status))

	assert.Equal(t, "deploy plan", plan["command"])
	assert.Equal(t, "deploy status", status["command"])
	assert.NotEqual(t, planOut, statusOut, "plan must not be an alias of status")

	// plan carries the change set; status carries the observation.
	assert.Nil(t, status["deploy_plan"])
	assert.Nil(t, plan["deployment"])
	planPayload := plan["deploy_plan"].(map[string]any)
	assert.Equal(t, "aaaabbbbccccdddd0000", planPayload["plan_hash"])
	assert.Equal(t, "obs-1", planPayload["observed_state_hash"])
	changes := planPayload["changes"].([]any)
	require.Len(t, changes, 2)
	assert.Equal(t, "deploy://fake/app", changes[0].(map[string]any)["path"])
	assert.Equal(t, "deploy://fake/secrets", changes[1].(map[string]any)["path"])
	// The envelope's fixed change vocabulary carries the same paths.
	envelopeChanges := plan["changes"].([]any)
	require.Len(t, envelopeChanges, 2)
	assert.Equal(t, "remote", envelopeChanges[0].(map[string]any)["class"])
	// Secret keys appear by name only; no value ever does.
	assert.Contains(t, planOut, "DATABASE_URL")
	assert.NotContains(t, planOut, "postgres://")
}

// TestDeployApplyRefusesStalePlan keeps the observed-hash gate: a target whose
// fresh Status disagrees with the confirmed plan refuses instead of applying.
func TestDeployApplyRefusesStalePlan(t *testing.T) {
	target := &fakeDeployTarget{
		planHash: "aaaabbbbccccdddd0000", planObserved: "obs-1", planChanges: deployFixtureChanges(),
		statusObserved: "obs-2",
	}
	_, app := remoteFixture(t, target)

	_, _, err := runAppWith(t, app, "deploy", "apply", "--environment", "production", "--yes")

	require.Error(t, err)
	assert.Equal(t, 3, exitOf(t, err))
	assert.Contains(t, err.Error(), "remote_plan_stale")
	assert.Zero(t, target.applied)
}

// TestReadOnlyRemoteCommandsRenderInHumanMode holds that a human-mode
// invocation of the payload-shaped read-only remote commands prints their
// data. Their JSON envelopes are unchanged; only the human rendering is new.
func TestReadOnlyRemoteCommandsRenderInHumanMode(t *testing.T) {
	target := &fakeDeployTarget{
		planHash: "aaaabbbbccccdddd0000", planObserved: "obs-1", planChanges: deployFixtureChanges(),
		statusState: "running", statusReady: true,
	}
	_, app := remoteFixture(t, target)

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "provider list",
			args: []string{"provider", "list"},
			want: []string{"ggg/mail", "development", "ggg/system/mail-dev@filesystem", "MAIL_DEV_DIR"},
		},
		{
			name: "provider test",
			args: []string{"provider", "test", "--slot", "ggg/mail", "--environment", "development"},
			want: []string{"ggg/mail", "development", "ggg/system/mail-dev@filesystem", "manual"},
		},
		{
			name: "deploy status",
			args: []string{"deploy", "status", "--environment", "production"},
			want: []string{"ggg/system/deploy-fake", "fake", "production", "ready", "running", "rel-8"},
		},
		{
			name: "deploy plan",
			args: []string{"deploy", "plan", "--environment", "production"},
			want: []string{"ggg/system/deploy-fake", "aaaabbbbccccdddd0000", "obs-1", "deploy://fake/app", "deploy://fake/secrets"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runAppWith(t, app, tc.args...)

			require.NoError(t, err)
			require.NotEmpty(t, out, "human mode must not print an empty result")
			for _, want := range tc.want {
				assert.Contains(t, out, want)
			}
		})
	}
}
