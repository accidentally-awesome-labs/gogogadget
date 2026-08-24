package modkit

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
	ID            string                `json:"id"`
	Kind          ModuleKind            `json:"kind"`
	Name          string                `json:"name"`
	Revision      int                   `json:"revision"`
	Contract      int                   `json:"contract"`
	Title         string                `json:"title"`
	Description   string                `json:"description"`
	Requires      []string              `json:"requires"`
	Files         []ManifestFile        `json:"files"`
	Claims        NamespaceClaims       `json:"claims"`
	Runtime       RuntimeContributions  `json:"runtime"`
	Migrations    []ManifestMigration   `json:"migrations"`
	Environment   []EnvironmentVariable `json:"environment"`
	Docs          []DocumentationRef    `json:"docs"`
	Tests         TestMetadata          `json:"tests"`
	Data          []DataDeclaration     `json:"data"`
	RemovalPolicy RemovalPolicy         `json:"removal_policy"`
	TestOnly      bool                  `json:"test_only,omitempty"`
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
	Start       bool             `json:"start"`
	Stop        bool             `json:"stop"`
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
	ID       string   `json:"id"`
	Area     NavArea  `json:"area"`
	RouteID  string   `json:"route_id"`
	LabelKey string   `json:"label_key"`
	Before   []string `json:"before,omitempty"`
	After    []string `json:"after,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Flags    []string `json:"flags,omitempty"`
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
	Key                string `json:"key"`
	Field              string `json:"field"`
	Description        string `json:"description"`
	Default            string `json:"default,omitempty"`
	Required           bool   `json:"required,omitempty"`
	ProductionRequired bool   `json:"production_required,omitempty"`
	Secret             bool   `json:"secret,omitempty"`
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
