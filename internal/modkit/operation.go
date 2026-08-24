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
}
