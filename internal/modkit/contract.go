package modkit

import (
	"encoding/json"
	"fmt"
)

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

// EngineContract is the behavior this engine build guarantees to a lock it
// reads. Every written lock records it, and a reader refuses a lock that
// records a higher one: that resolved state was produced by an engine which
// knows capabilities, generators, or invariants this binary does not, so
// proceeding misreports the tree instead of naming the stale binary.
//
// Bump discipline: raise it by one in the same change that makes an older
// binary wrong about a lock this engine writes — a new generated capability
// provider or consumer, a new generator output, a new resolver invariant.
// Leave it alone for changes an older binary still reads correctly (wording,
// performance, new commands). The lock file format is versioned separately by
// Lock.Schema, and the two move independently.
//
//	1 — provider-aware schema 2: scoped ids, provider slots, runtime orders.
//	2 — the runtime.health capability: locks assume a health provider exists.
const EngineContract = 2

// EngineContractError reports a lock written by a newer engine than the binary
// reading it. It carries the declared refusal code so no reporting layer can
// relabel the one diagnostic that names the remedy, and every lock reader
// returns it before planning, generating, or writing anything.
type EngineContractError struct {
	// Lock is the engine contract the lock file records.
	Lock int
	// Binary is the engine contract this build compiles in.
	Binary int
}

func (e EngineContractError) Error() string {
	return fmt.Sprintf(
		"%s records engine contract %d; this ggg binary is contract %d. "+
			"Rebuild the CLI from this tree — `go build -o bin/ggg ./cmd/ggg`, or `make setup` — then re-run",
		LockFileName, e.Lock, e.Binary)
}

// ExitCode reports the refusal code: nothing was planned and nothing written.
func (e EngineContractError) ExitCode() int { return ExitRefusal }

// EngineContractRefusal reports the refusal for the contract a lock records,
// or nil when this binary may read it. Zero means the lock predates the guard,
// which is the oldest contract there is and always readable. It is the single
// implementation of the comparison, so no caller can get the direction wrong.
func EngineContractRefusal(recorded int) error {
	if recorded > EngineContract {
		return EngineContractError{Lock: recorded, Binary: EngineContract}
	}
	return nil
}

// LockEngineContract reads only the engine contract a lock records. Commands
// that would write before parsing the lock in full use it to refuse first; a
// document too malformed to yield this field reports zero and is left to the
// full reader, which names the malformation.
func LockEngineContract(data []byte) int {
	var header struct {
		EngineContract int `json:"engine_contract"`
	}
	if json.Unmarshal(data, &header) != nil {
		return 0
	}
	return header.EngineContract
}
