package modkit

import "encoding/json"

// Project is the hand-owned declaration of registry intent.
type Project struct {
	Schema   int             `json:"schema"`
	Registry ProjectRegistry `json:"registry"`
	Modules  []string        `json:"modules"`
	Exclude  []string        `json:"exclude"`
}

// ProjectRegistry identifies the upstream registry and requested ref.
type ProjectRegistry struct {
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
}

// ModuleKind is the closed set of installable manifest kinds.
type ModuleKind string

const (
	ModuleElement   ModuleKind = "element"
	ModuleComponent ModuleKind = "component"
	ModulePage      ModuleKind = "page"
	ModuleWorkflow  ModuleKind = "workflow"
	ModuleSystem    ModuleKind = "system"
)

// RemovalPolicy describes the preconditions and retained state of removal.
type RemovalPolicy string

const (
	RemovalFree                RemovalPolicy = "free"
	RemovalRetainData          RemovalPolicy = "retain-data"
	RemovalDrainRequired       RemovalPolicy = "drain-required"
	RemovalReplacementRequired RemovalPolicy = "replacement-required"
	RemovalMajorVersionOnly    RemovalPolicy = "major-version-only"
)

// FileState describes the relationship between a local file and its installed base.
type FileState string

const (
	FileClean      FileState = "clean"
	FileModified   FileState = "modified"
	FileMissing    FileState = "missing"
	FileConflicted FileState = "conflicted"
)

// FileClass determines how a registry payload participates in generation.
type FileClass string

const (
	FileClassGo        FileClass = "go"
	FileClassTempl     FileClass = "templ"
	FileClassStyle     FileClass = "style"
	FileClassScript    FileClass = "script"
	FileClassAsset     FileClass = "asset"
	FileClassQuery     FileClass = "query"
	FileClassMigration FileClass = "migration"
	FileClassI18n      FileClass = "i18n"
	FileClassContent   FileClass = "content"
	FileClassSeed      FileClass = "seed"
	FileClassDocs      FileClass = "docs"
	FileClassTest      FileClass = "test"
	FileClassOpenAPI   FileClass = "openapi"
)

// MigrationKind identifies the forward-only purpose of a migration payload.
type MigrationKind string

const (
	MigrationImmutable  MigrationKind = "immutable"
	MigrationNeutralize MigrationKind = "neutralize"
	MigrationPurge      MigrationKind = "purge"
)

// DataScope identifies who owns a stateful row.
type DataScope string

const (
	DataScopeUser     DataScope = "user"
	DataScopeOrg      DataScope = "org"
	DataScopePlatform DataScope = "platform"
)

// DeleteBehavior defines what deletion of a scope owner does to state.
type DeleteBehavior string

const (
	DeleteCascade DeleteBehavior = "cascade"
	DeleteManual  DeleteBehavior = "manual"
	DeleteRetain  DeleteBehavior = "retain"
)

// RouteScope is the closed middleware/security profile for a route.
type RouteScope string

const (
	RoutePublic   RouteScope = "public"
	RouteApp      RouteScope = "app"
	RouteAdmin    RouteScope = "admin"
	RouteAPIRead  RouteScope = "api-read"
	RouteAPIWrite RouteScope = "api-write"
	RouteWebhook  RouteScope = "webhook"
	RouteStatic   RouteScope = "static"
	RouteProbe    RouteScope = "probe"
	RouteDev      RouteScope = "dev"
)

// ContentMode selects the generated content route shape.
type ContentMode string

const (
	ContentModePages  ContentMode = "pages"
	ContentModeSingle ContentMode = "single"
)

// NavArea is the closed set of generated navigation regions.
type NavArea string

const (
	NavAreaPublic   NavArea = "public"
	NavAreaApp      NavArea = "app"
	NavAreaAdmin    NavArea = "admin"
	NavAreaFooter   NavArea = "footer"
	NavAreaSettings NavArea = "settings"
)

// ShellSlot is the closed set of generated shell extension points.
type ShellSlot string

const (
	ShellSlotHead            ShellSlot = "head"
	ShellSlotAppBanner       ShellSlot = "app-banner"
	ShellSlotSidebar         ShellSlot = "sidebar"
	ShellSlotTopbar          ShellSlot = "topbar"
	ShellSlotPersistentBody  ShellSlot = "persistent-body"
	ShellSlotDashboardWidget ShellSlot = "dashboard-widget"
	ShellSlotSettingsSection ShellSlot = "settings-section"
	ShellSlotAdminRowAction  ShellSlot = "admin-row-action"
	ShellSlotBillingUsage    ShellSlot = "billing-usage"
	ShellSlotContentEditor   ShellSlot = "content-editor"
)

// GalleryFamily is the closed component-reference family set.
type GalleryFamily string

const (
	GalleryFoundations   GalleryFamily = "foundations"
	GalleryActions       GalleryFamily = "actions"
	GalleryForms         GalleryFamily = "forms"
	GalleryNavigation    GalleryFamily = "navigation"
	GalleryFeedback      GalleryFamily = "feedback"
	GalleryOverlays      GalleryFamily = "overlays"
	GalleryData          GalleryFamily = "data"
	GalleryCommunication GalleryFamily = "communication"
	GalleryLayout        GalleryFamily = "layout"
	GalleryAdvanced      GalleryFamily = "advanced"
)

// AssetKind is the closed static asset role set.
type AssetKind string

const (
	AssetScript AssetKind = "script"
	AssetStyle  AssetKind = "style"
	AssetFont   AssetKind = "font"
	AssetImage  AssetKind = "image"
	AssetFile   AssetKind = "file"
)

// Manifest is the canonical, embedded snapshot of one resolved module.
type Manifest struct {
	ID          string                `json:"id"`
	Kind        ModuleKind            `json:"kind"`
	Name        string                `json:"name"`
	Revision    int                   `json:"revision"`
	Contract    int                   `json:"contract"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Requires    []string              `json:"requires"`
	Files       []ManifestFile        `json:"files"`
	Claims      NamespaceClaims       `json:"claims"`
	Runtime     RuntimeContributions  `json:"runtime"`
	Migrations  []ManifestMigration   `json:"migrations"`
	Environment []EnvironmentVariable `json:"environment"`
	// Locales maps a locale code to the keys this module owns in that locale.
	// A module's own translations travel with it, so installing a page installs
	// its strings and removing it removes them. Ownership is exclusive: two
	// modules declaring one key would make the rendered string depend on which
	// other modules happen to be installed.
	Locales map[string]map[string]string `json:"locales,omitempty"`
	// OpenAPI is this module's slice of the /api/v1 contract, when it serves any.
	OpenAPI       *OpenAPIContribution `json:"openapi,omitempty"`
	Docs          []DocumentationRef   `json:"docs"`
	Tests         TestMetadata         `json:"tests"`
	Data          []DataDeclaration    `json:"data"`
	RemovalPolicy RemovalPolicy        `json:"removal_policy"`
	TestOnly      bool                 `json:"test_only,omitempty"`
}

// ManifestFile maps one verified registry payload to one project-owned target.
type ManifestFile struct {
	Source        string    `json:"source"`
	Target        string    `json:"target"`
	Class         FileClass `json:"class"`
	SHA256        string    `json:"sha256"`
	RewriteModule bool      `json:"rewrite_module"`
	Contract      bool      `json:"contract"`
}

// NamespaceClaims contains collision-checked names. Empty claims encode as {}.
type NamespaceClaims struct {
	Packages     []string `json:"packages,omitempty"`
	Routes       []string `json:"routes,omitempty"`
	Jobs         []string `json:"jobs,omitempty"`
	Environment  []string `json:"environment,omitempty"`
	I18n         []string `json:"i18n,omitempty"`
	Queries      []string `json:"queries,omitempty"`
	OpenAPI      []string `json:"openapi,omitempty"`
	ContentTypes []string `json:"content_types,omitempty"`
	UI           []string `json:"ui,omitempty"`
	Assets       []string `json:"assets,omitempty"`
	Data         []string `json:"data,omitempty"`
}

// RuntimeContributions is the typed, data-only runtime declaration. Empty
// contributions encode as {}.
type RuntimeContributions struct {
	System       *SystemContribution       `json:"system,omitempty"`
	Routes       []RouteContribution       `json:"routes,omitempty"`
	Jobs         []JobContribution         `json:"jobs,omitempty"`
	Janitors     []JanitorContribution     `json:"janitors,omitempty"`
	Queries      []QueryContribution       `json:"queries,omitempty"`
	ContentTypes []ContentTypeContribution `json:"content_types,omitempty"`
	Navigation   []NavigationContribution  `json:"navigation,omitempty"`
	Slots        []SlotContribution        `json:"slots,omitempty"`
	UI           []UIContribution          `json:"ui,omitempty"`
	Assets       []AssetContribution       `json:"assets,omitempty"`
}

// SystemContribution declares a directly compiled runtime constructor.
type SystemContribution struct {
	Package     string           `json:"package"`
	Constructor string           `json:"constructor"`
	Needs       []RuntimeNeed    `json:"needs"`
	Provides    []RuntimeProvide `json:"provides"`
	// TypeImports are the extra packages the provided type expressions need —
	// a pool, a generated query struct. The generated bootstrap imports module
	// packages automatically, but a provided type can come from a package the
	// module merely uses, and an unimported type is a compile error in a
	// DO-NOT-EDIT file. A path whose first segment contains a dot is an external
	// module path used verbatim; anything else is module-relative and qualified
	// against the target module, exactly like Package.
	TypeImports []string `json:"type_imports,omitempty"`
	Start       bool     `json:"start"`
	Stop        bool     `json:"stop"`
}

// RuntimeNeed is one typed constructor dependency.
type RuntimeNeed struct {
	Field      string `json:"field"`
	Capability string `json:"capability"`
	Type       string `json:"type"`
	Optional   bool   `json:"optional"`
}

// RuntimeProvide is one typed value exported by a system module.
type RuntimeProvide struct {
	Field      string `json:"field"`
	Capability string `json:"capability"`
	Type       string `json:"type"`
}

// RouteContribution declares a concrete generated route.
type RouteContribution struct {
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Pattern string      `json:"pattern"`
	Scope   RouteScope  `json:"scope"`
	Policy  RoutePolicy `json:"policy"`
	Package string      `json:"package"`
	Handler string      `json:"handler"`
	Enabled string      `json:"enabled,omitempty"`
}

// RoutePolicy is the closed security and transport policy for a route.
type RoutePolicy struct {
	CSRFExempt        bool   `json:"csrf_exempt"`
	CSRFReason        string `json:"csrf_reason"`
	RateExempt        bool   `json:"rate_exempt"`
	MaintenanceExempt bool   `json:"maintenance_exempt"`
	MaxBodyBytes      int64  `json:"max_body_bytes"`
	Idempotent        bool   `json:"idempotent"`
	AdminWrite        bool   `json:"admin_write"`
}

// JobContribution declares one typed generated job definition.
type JobContribution struct {
	Kind        string `json:"kind"`
	Package     string `json:"package"`
	Handler     string `json:"handler"`
	Schedulable bool   `json:"schedulable"`
	MaxAttempts int    `json:"max_attempts"`
}

// JanitorContribution declares one recurring cleanup sweep. Each sweep deletes
// from a table its module owns, so it is declared alongside that table rather
// than hard-coded in the worker: uninstalling the module removes the sweep with
// it, instead of leaving a call to a query that no longer exists.
type JanitorContribution struct {
	Name    string `json:"name"`
	Package string `json:"package"`
	Handler string `json:"handler"`
}

// QueryContribution declares one sqlc method this module owns and the table it
// reads or writes. The generated sqlc package is one flat namespace shared by
// every module, so without declarations two modules can collide on a method
// name, and a module can quietly depend on a table that disappears when another
// module is removed.
type QueryContribution struct {
	// Name is the sqlc method name, which is the `-- name:` annotation in the
	// query file and the Go method it generates.
	Name string `json:"name"`
	// Table is the table the query reads or writes. It must be declared by some
	// module's data records; reading a table owned by another module requires a
	// dependency on that module.
	Table string `json:"table"`
}

// ContentTypeContribution declares generated content routes and their handler.
type ContentTypeContribution struct {
	ID      string      `json:"id"`
	Mode    ContentMode `json:"mode"`
	Paths   []string    `json:"paths"`
	Package string      `json:"package"`
	Handler string      `json:"handler"`
}

// NavigationContribution declares a generated navigation entry.
type NavigationContribution struct {
	ID   string  `json:"id"`
	Area NavArea `json:"area"`
	// RouteID names the route this entry links to, so the href comes from the
	// same records the router is built from and a nav link cannot point at a
	// route that does not exist. Empty only for a target that is not a route:
	// an in-page anchor.
	RouteID string `json:"route_id,omitempty"`
	// Href is the literal target for a non-route entry. Exactly one of RouteID
	// and Href is set.
	Href string `json:"href,omitempty"`
	// Match is the path prefix that marks this entry current. Empty means the
	// href itself, which is right for a leaf and wrong for a section root.
	Match    string `json:"match,omitempty"`
	LabelKey string `json:"label_key"`
	// Group is the footer column this entry belongs to, named by its title key.
	// Footer entries only.
	Group  string   `json:"group,omitempty"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
	Roles  []string `json:"roles,omitempty"`
	Flags  []string `json:"flags,omitempty"`
}

// OpenAPIContribution is a module's slice of the API contract. Operations are
// keyed by route id, so an operation cannot describe an endpoint the router does
// not serve and an endpoint cannot ship undocumented.
type OpenAPIContribution struct {
	// Info and Servers are document-level and must be supplied by exactly one
	// module; two would be an unresolvable disagreement about the same document.
	Info    json.RawMessage `json:"info,omitempty"`
	Servers json.RawMessage `json:"servers,omitempty"`
	Tags    []OpenAPITag    `json:"tags,omitempty"`
	// Components is section -> id -> definition, merged across modules with
	// duplicate ids refused: two schemas under one name means callers get
	// whichever module happened to win.
	Components map[string]map[string]json.RawMessage `json:"components,omitempty"`
	Operations []OpenAPIOperation                    `json:"operations,omitempty"`
}

// OpenAPITag is a document-level tag description.
type OpenAPITag struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// OpenAPIOperation documents one declared route. Security and the idempotency
// parameter are derived from the route's declared scope and policy rather than
// restated here, so the document cannot disagree with what middleware enforces.
type OpenAPIOperation struct {
	RouteID     string          `json:"route_id"`
	OperationID string          `json:"operation_id"`
	Summary     string          `json:"summary"`
	Description string          `json:"description,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	RequestBody json.RawMessage `json:"request_body,omitempty"`
	Responses   json.RawMessage `json:"responses"`
}

// SlotContribution declares a typed renderer in a generated shell slot.
type SlotContribution struct {
	ID       string    `json:"id"`
	Slot     ShellSlot `json:"slot"`
	Package  string    `json:"package"`
	Renderer string    `json:"renderer"`
	Before   []string  `json:"before,omitempty"`
	After    []string  `json:"after,omitempty"`
}

// UIContribution is the generated component metadata supplied by a UI item.
type UIContribution struct {
	Name           string        `json:"name"`
	Family         GalleryFamily `json:"family"`
	Engine         string        `json:"engine,omitempty"`
	Alpine         string        `json:"alpine,omitempty"`
	Vendor         string        `json:"vendor,omitempty"`
	NativeFallback string        `json:"native_fallback,omitempty"`
}

// AssetContribution declares an ordered same-origin static asset.
type AssetContribution struct {
	ID     string    `json:"id"`
	Path   string    `json:"path"`
	Kind   AssetKind `json:"kind"`
	Before []string  `json:"before,omitempty"`
	After  []string  `json:"after,omitempty"`
}

// ManifestMigration declares a reviewed, forward-only migration payload.
type ManifestMigration struct {
	ID     string        `json:"id"`
	Kind   MigrationKind `json:"kind"`
	Source string        `json:"source"`
	SHA256 string        `json:"sha256"`
}

// EnvironmentVariable is one generated configuration declaration.
type EnvironmentVariable struct {
	Key   string `json:"key"`
	Field string `json:"field"`
	// Type is closed: the generator must know how to parse a value before it can
	// generate a parse for it, and an unrecognised type is a manifest bug rather
	// than something to guess a string for.
	Type        EnvType `json:"type"`
	Description string  `json:"description"`
	Default     string  `json:"default,omitempty"`
	// Example is what .env.example ships, when that differs from the default.
	// The two are not the same question: Default is what the code assumes when a
	// key is absent, Example is what makes a fresh clone work. DEV_AUTH_BYPASS
	// defaults to false because forged sessions must never be the fallback, and
	// ships true because /dev/login is how a developer with no Clerk account
	// signs in.
	Example string `json:"example,omitempty"`
	// Min and Max bound an int. Pointers because zero is a meaningful bound:
	// AUDIT_RETENTION_DAYS accepts 0 (retain forever) while RATE_LIMIT_RPM does
	// not, and a plain int cannot tell "0" from "unset".
	Min *int `json:"min,omitempty"`
	Max *int `json:"max,omitempty"`
	// Enum closes a string to a known set. An unknown value is refused rather
	// than silently behaving like the default.
	Enum []string `json:"enum,omitempty"`
	// TrimSlash strips a trailing slash so a URL joins predictably whether or
	// not the operator typed one.
	TrimSlash          bool `json:"trim_slash,omitempty"`
	Required           bool `json:"required,omitempty"`
	ProductionRequired bool `json:"production_required,omitempty"`
	Secret             bool `json:"secret,omitempty"`
}

// EnvType is the parse a declared key needs.
type EnvType string

const (
	EnvString EnvType = "string"
	EnvInt    EnvType = "int"
	EnvBool   EnvType = "bool"
	// EnvTime is RFC3339. Whether a parsed time is honoured is behaviour, not
	// data, so it stays with the code that reads the field.
	EnvTime EnvType = "time"
)

// EnvTypes is every parse the generator can emit.
var EnvTypes = []EnvType{EnvString, EnvInt, EnvBool, EnvTime}

// Valid reports whether the generator knows how to parse this type.
func (t EnvType) Valid() bool {
	for _, known := range EnvTypes {
		if t == known {
			return true
		}
	}
	return false
}

// DocumentationRef links a module to hand-owned explanatory documentation.
type DocumentationRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// TestMetadata identifies generated verification inventory. Empty metadata
// encodes as {}.
type TestMetadata struct {
	GoPackages    []string `json:"go_packages,omitempty"`
	Smoke         []string `json:"smoke,omitempty"`
	E2E           []string `json:"e2e,omitempty"`
	Visual        []string `json:"visual,omitempty"`
	Accessibility []string `json:"accessibility,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

// DataDeclaration describes lifecycle obligations for state owned by a module.
type DataDeclaration struct {
	Table                string         `json:"table"`
	RowDiscriminator     string         `json:"row_discriminator,omitempty"`
	Scope                DataScope      `json:"scope"`
	Export               bool           `json:"export"`
	SecretRedactionOwner string         `json:"secret_redaction_owner,omitempty"`
	AccountDelete        DeleteBehavior `json:"account_delete"`
	OrganizationDelete   DeleteBehavior `json:"organization_delete"`
	ExternalObjects      []string       `json:"external_objects,omitempty"`
	PersistedJobs        []string       `json:"persisted_jobs,omitempty"`
}

// Lock is the generated, committed resolved registry state.
type Lock struct {
	Schema         int            `json:"schema"`
	RegistryCommit string         `json:"registry_commit"`
	Order          []string       `json:"order"`
	Modules        []LockedModule `json:"modules"`
}

// LockedModule is one installed module and its canonical manifest snapshot.
type LockedModule struct {
	ID           string            `json:"id"`
	Revision     int               `json:"revision"`
	Contract     int               `json:"contract"`
	SourceCommit string            `json:"source_commit"`
	Reason       string            `json:"reason"`
	RequiredBy   []string          `json:"required_by"`
	Manifest     Manifest          `json:"manifest"`
	Files        []LockedFile      `json:"files"`
	Migrations   []LockedMigration `json:"migrations"`
	Pending      *PendingUpdate    `json:"pending,omitempty"`
}

// LockedFile records the installed base and current local bytes of one target.
type LockedFile struct {
	Path        string    `json:"path"`
	Source      string    `json:"source"`
	BaseSHA256  string    `json:"base_sha256"`
	LocalSHA256 string    `json:"local_sha256"`
	State       FileState `json:"state"`
}

// LockedMigration is the immutable global-number mapping for a logical migration.
type LockedMigration struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// PendingUpdate records a verified upstream candidate that is awaiting conflict
// resolution.
type PendingUpdate struct {
	RunID          string            `json:"run_id"`
	RegistryCommit string            `json:"registry_commit"`
	SourceCommit   string            `json:"source_commit"`
	Manifest       Manifest          `json:"manifest"`
	Conflicts      []PendingConflict `json:"conflicts"`
}

// PendingConflict points to ignored, re-materializable candidate and diff data.
type PendingConflict struct {
	Path            string `json:"path"`
	CandidateSHA256 string `json:"candidate_sha256"`
	CandidatePath   string `json:"candidate_path"`
	DiffPath        string `json:"diff_path"`
}
