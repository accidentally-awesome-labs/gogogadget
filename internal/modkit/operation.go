package modkit

// OperationKind identifies a registry operation.
type OperationKind string

const (
	OpInit   OperationKind = "init"
	OpAdd    OperationKind = "add"
	OpUpdate OperationKind = "update"
	OpRemove OperationKind = "remove"
	OpSync   OperationKind = "sync"
)

// Operation describes a requested registry change.
type Operation struct {
	Kind        OperationKind
	Modules     []string
	RegistryRef string
	DryRun      bool
	Offline     bool
	PurgeData   bool
	// Claims are project paths the operator explicitly claims during adoption.
	// A pre-existing file that diverges from its registry payload is unowned and
	// blocks adoption; claiming it adopts the local bytes as a recorded
	// modification instead of overwriting them or calling them pristine.
	Claims []string
	// SetDeployment replaces the project's deployment selection as part of one
	// planned transaction: the newly selected deploy module enters the graph
	// with reason "deployment" and the previously selected one is retired in
	// the same plan — its authored files deleted, its migration ledger
	// tombstoned — because a tree cannot carry two selected deploy modules.
	SetDeployment string
	// SetProviders replaces the project's provider selections in the same
	// planned transaction. Adapters the new selections stop selecting are
	// retired exactly like a replaced deployment module.
	SetProviders map[string]ProviderSelections
}
