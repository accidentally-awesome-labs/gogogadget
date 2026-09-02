package remote

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StateFilePath is the ignored CLI state file, relative to the project root.
// It records resource ids, observed hashes, and check timestamps. It never
// records secret values: renderers and state dumps can print it verbatim.
func StateFilePath() string {
	return filepath.Join(EnvDirName, StateFileName)
}

// ChangeRunState tracks one remote change inside a persisted run: pending
// before its apply, applied after. A resumed run replays only pending
// changes.
type ChangeRunState struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// RunState is one persisted remote operation. PlanHash is the confirmed
// plan the run resumes with — resume replans nothing — and ChangeStatus
// records how far a partial apply reached.
type RunState struct {
	RunID             string            `json:"run_id"`
	Kind              string            `json:"kind"`
	Command           string            `json:"command"`
	Environment       string            `json:"environment"`
	PlanHash          string            `json:"plan_hash"`
	ObservedStateHash string            `json:"observed_state_hash"`
	Changes           []ChangeRunState  `json:"changes"`
	ResourceIDs       map[string]string `json:"resource_ids,omitempty"`
	StartedAt         time.Time         `json:"started_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

// Running reports whether the run still has pending changes.
func (r RunState) Running() bool {
	for _, change := range r.Changes {
		if change.Status != ChangeApplied {
			return true
		}
	}
	return false
}

// Change run status values.
const (
	ChangePending = "pending"
	ChangeApplied = "applied"
)

// ProviderEnvState is one provisioned adapter target's persisted state.
type ProviderEnvState struct {
	ResourceIDs       map[string]string `json:"resource_ids"`
	ObservedStateHash string            `json:"observed_state_hash"`
	CheckedAt         time.Time         `json:"checked_at"`
}

// DeployEnvState is one deployed environment's persisted state.
type DeployEnvState struct {
	ReleaseID       string            `json:"release_id"`
	URL             string            `json:"url"`
	ImageDigest     string            `json:"image_digest"`
	ObservedVersion string            `json:"observed_version"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// BackupRecord is one completed backup the state store remembers, so
// restore and drill can address it by id. Locations and digests only —
// never secret values.
type BackupRecord struct {
	ID        string    `json:"id"`
	Location  string    `json:"location"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
}

// State is the on-disk shape of .ggg/state.json.
type State struct {
	Version   int                         `json:"version"`
	Runs      map[string]RunState         `json:"runs"`
	Providers map[string]ProviderEnvState `json:"providers"`
	Deploys   map[string]DeployEnvState   `json:"deploys"`
	Backups   map[string]BackupRecord     `json:"backups"`
	Checks    map[string]time.Time        `json:"checks"`
}

// stateVersion is the current on-disk version. A future format bump refuses
// rather than guesses at old bytes.
const stateVersion = 1

// LoadState reads .ggg/state.json from the project root. A missing file is
// an empty state, not an error.
func LoadState(root string) (*State, error) {
	path := filepath.Join(root, StateFilePath())
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return newState(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", StateFilePath(), err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StateFilePath(), err)
	}
	if state.Version != stateVersion {
		return nil, fmt.Errorf("%s has unsupported version %d", StateFilePath(), state.Version)
	}
	if state.Runs == nil {
		state.Runs = map[string]RunState{}
	}
	if state.Providers == nil {
		state.Providers = map[string]ProviderEnvState{}
	}
	if state.Deploys == nil {
		state.Deploys = map[string]DeployEnvState{}
	}
	if state.Backups == nil {
		state.Backups = map[string]BackupRecord{}
	}
	if state.Checks == nil {
		state.Checks = map[string]time.Time{}
	}
	return &state, nil
}

func newState() *State {
	return &State{
		Version:   stateVersion,
		Runs:      map[string]RunState{},
		Providers: map[string]ProviderEnvState{},
		Deploys:   map[string]DeployEnvState{},
		Backups:   map[string]BackupRecord{},
		Checks:    map[string]time.Time{},
	}
}

// Save atomically writes the state file: the temp file is fsynced and
// renamed, so a crash never leaves a half-written state behind. Mode 0600:
// resource ids and release urls are operationally sensitive even though
// they are not secret values.
func (s *State) Save(root string) error {
	path := filepath.Join(root, StateFilePath())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// RecordProvider persists one provisioned target's state and check time.
func (s *State) RecordProvider(slot, environment string, state ProviderState) {
	key := slot + "@" + environment
	if state.ResourceIDs == nil {
		state.ResourceIDs = map[string]string{}
	}
	s.Providers[key] = ProviderEnvState{
		ResourceIDs:       state.ResourceIDs,
		ObservedStateHash: state.ObservedStateHash,
		CheckedAt:         time.Now().UTC(),
	}
}

// ProviderState reads one provisioned target's persisted state.
func (s *State) ProviderState(slot, environment string) (ProviderState, bool) {
	key := slot + "@" + environment
	record, ok := s.Providers[key]
	if !ok {
		return ProviderState{}, false
	}
	ids := make(map[string]string, len(record.ResourceIDs))
	for id, value := range record.ResourceIDs {
		ids[id] = value
	}
	return ProviderState{ResourceIDs: ids, ObservedStateHash: record.ObservedStateHash}, true
}

// RecordDeploy persists one environment's deployed state.
func (s *State) RecordDeploy(environment string, state DeployState) {
	if state.Metadata == nil {
		state.Metadata = map[string]string{}
	}
	s.Deploys[environment] = DeployEnvState{
		ReleaseID: state.ReleaseID, URL: state.URL, ImageDigest: state.ImageDigest,
		ObservedVersion: state.ObservedVersion, Metadata: state.Metadata,
		UpdatedAt: time.Now().UTC(),
	}
}

// DeployState reads one environment's persisted deploy state.
func (s *State) DeployState(environment string) (DeployState, bool) {
	record, ok := s.Deploys[environment]
	if !ok {
		return DeployState{}, false
	}
	metadata := make(map[string]string, len(record.Metadata))
	for key, value := range record.Metadata {
		metadata[key] = value
	}
	return DeployState{
		ReleaseID: record.ReleaseID, URL: record.URL, ImageDigest: record.ImageDigest,
		ObservedVersion: record.ObservedVersion, Metadata: metadata,
	}, true
}

// StartRun persists a new run for the confirmed plan and returns it. The run
// is the resume record: `--resume RUN_ID` loads exactly these bytes.
func (s *State) StartRun(runID, kind, command, environment, planHash, observedHash string, changes []RemoteChange) RunState {
	entries := make([]ChangeRunState, 0, len(changes))
	for _, change := range changes {
		entries = append(entries, ChangeRunState{Path: change.Path, Status: ChangePending})
	}
	now := time.Now().UTC()
	run := RunState{
		RunID: runID, Kind: kind, Command: command, Environment: environment,
		PlanHash: planHash, ObservedStateHash: observedHash,
		Changes: entries, StartedAt: now, UpdatedAt: now,
	}
	s.Runs[runID] = run
	return run
}

// MarkChange records one change's completion inside a run.
func (s *State) MarkChange(runID, path string, applied bool) error {
	run, ok := s.Runs[runID]
	if !ok {
		return fmt.Errorf("run %s is not recorded", runID)
	}
	status := ChangePending
	if applied {
		status = ChangeApplied
	}
	for i := range run.Changes {
		if run.Changes[i].Path == path {
			run.Changes[i].Status = status
			run.UpdatedAt = time.Now().UTC()
			s.Runs[runID] = run
			return nil
		}
	}
	return fmt.Errorf("run %s has no change at %s", runID, path)
}

// PendingChanges lists the change paths a resumed run still owes, in run
// order — the plan's order, not a re-sorted one.
func (r RunState) PendingChanges() []string {
	paths := make([]string, 0, len(r.Changes))
	for _, change := range r.Changes {
		if change.Status != ChangeApplied {
			paths = append(paths, change.Path)
		}
	}
	return paths
}

// RecordBackup remembers one completed backup artifact by id.
func (s *State) RecordBackup(backup BackupRecord) {
	if s.Backups == nil {
		s.Backups = map[string]BackupRecord{}
	}
	s.Backups[backup.ID] = backup
}

// DropRun forgets a completed or abandoned run.
func (s *State) DropRun(runID string) { delete(s.Runs, runID) }

// ActiveRun returns the newest run that still owes changes, if any.
func (s *State) ActiveRun() (RunState, bool) {
	ids := make([]string, 0, len(s.Runs))
	for id := range s.Runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var newest RunState
	found := false
	for _, id := range ids {
		run := s.Runs[id]
		if !run.Running() {
			continue
		}
		if !found || run.UpdatedAt.After(newest.UpdatedAt) {
			newest, found = run, true
		}
	}
	return newest, found
}
