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
}
