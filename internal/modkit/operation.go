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
	// TargetedRegistry names the one registry whose ref a targeted update
	// changes. It requires RegistryRef and refuses module operands: changing
	// a ref permits exactly one registry and no named modules.
	TargetedRegistry string
	DryRun           bool
	Offline          bool
	PurgeData        bool
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
	// SetRegistries replaces the project's registry sources in the same
	// planned transaction the `ggg registry add|remove|update` flows use.
	SetRegistries []ProjectRegistry
	// SetExclude replaces the project's exclude list in the same planned
	// transaction. `registry remove` uses it to sequence its own tombstones:
	// the exclude entries of the namespace being removed must leave with it,
	// or the very next plan refuses on an exclude entry no configured
	// registry can resolve.
	SetExclude []string
	// PruneTombstones forgets tombstones that retain nothing a future plan can
	// use: no migration ledger, and a module no configured registry publishes
	// any more. It is OPT-IN, and that is the whole design.
	//
	// A tombstone is the lock remembering that a module was installed, what
	// its removal policy was, and which migration numbers it owns forever —
	// `assertLockRestored` in example.go enforces exactly that, so an
	// automatic prune is a rule that deletes a record another gate requires.
	// It was one, briefly: pruning inside reconcilePlannedState made
	// `ggg registry validate` refuse with "lock has no record of removed
	// module ggg/element/example-token", because the validator removes an
	// example and then unpublishes it, which is the same shape as retiring a
	// module for real.
	//
	// So this follows the posture the registry cache already sets — never
	// auto-prune something the lock references, add an explicit verb — and a
	// bare `ggg sync` carries every tombstone forward verbatim.
	PruneTombstones bool
}
