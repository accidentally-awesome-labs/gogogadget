package modkit

// Project and lock file names. These are a public contract: operators and
// automation reference them by name.
const (
	ProjectFileName = "gogogadget.json"
	LockFileName    = "gogogadget.lock.json"
)

// Envelope is the noninteractive result contract. Its key set is fixed: machine
// consumers depend on every field being present, so absent data encodes as an
// empty collection rather than a missing key.
type Envelope struct {
	OK             bool         `json:"ok"`
	Command        string       `json:"command"`
	RunID          string       `json:"run_id"`
	RegistryCommit string       `json:"registry_commit"`
	Resolved       []string     `json:"resolved"`
	Changes        []Change     `json:"changes"`
	Generated      []string     `json:"generated"`
	Conflicts      []Conflict   `json:"conflicts"`
	Diagnostics    []Diagnostic `json:"diagnostics"`
	Exit           int          `json:"exit"`
}

// Declared exit codes. These are a public contract: automation branches on
// them, and the gggcli presentation layer is their only producer.
const (
	ExitOK       = 0
	ExitRuntime  = 1
	ExitUsage    = 2
	ExitRefusal  = 3
	ExitConflict = 4
	ExitRollback = 5
)
