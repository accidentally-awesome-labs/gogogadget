package modkit

import "encoding/json"

// Project is the hand-owned declaration of registry intent.
type Project struct {
	Schema     int                           `json:"schema"`
	Registries []ProjectRegistry             `json:"registries"`
	Modules    []string                      `json:"modules"`
	Exclude    []string                      `json:"exclude"`
	Providers  map[string]ProviderSelections `json:"providers"`
	// Ports overrides the host port one generated Compose port publishes on.
	// A key is `<service>/<port>`: `app/http` for the generated app service,
	// and `<adapter>@<target>/<declared port name>` for an adapter's local
	// service. Absent keys keep the derived default.
	//
	// Publishing lives here, beside the provider selections that decide which
	// services exist at all, because it is a committed project decision: a
	// generated Compose file is registry-owned and a hand edit vanishes at the
	// next generate, and an environment variable would not be reviewable.
	// Nothing here is a secret.
	//
	// The whole key is optional, unlike the fixed keys above, and an absent
	// map is identical to an empty one everywhere. Overrides are intent an
	// older writer legitimately never wrote: a project file created before
	// this existed must keep loading, because it is data at rest that
	// upgrading a CLI cannot rewrite. Requiring the key would brick it, which
	// is the failure the schema contract exists to prevent — and it would be
	// a format change smuggled in under an unchanged schema version.
	Ports      map[string]PortOverrides `json:"ports,omitempty"`
	Deployment string                   `json:"deployment"`
}

// PortOverrides is the host port one declared Compose port publishes on, per
// generated Compose file. Production has no Compose file, so it has no field
// here; a zero value means the derived default stands.
type PortOverrides struct {
	Development int `json:"development,omitempty"`
	Test        int `json:"test,omitempty"`
}

// ForEnvironment returns the overridden host port for one environment, and
// whether an override was declared at all.
func (o PortOverrides) ForEnvironment(environment string) (int, bool) {
	switch environment {
	case "development":
		return o.Development, o.Development != 0
	case "test":
		return o.Test, o.Test != 0
	default:
		return 0, false
	}
}

// ProjectRegistry identifies one configured registry source.
type ProjectRegistry struct {
	Namespace  string `json:"namespace"`
	Source     string `json:"source"`
	Repository string `json:"repository,omitempty"`
	Path       string `json:"path,omitempty"`
	Ref        string `json:"ref,omitempty"`
	PublicKey  string `json:"public_key,omitempty"`
}

// ProviderSelections selects one adapter target in each runtime environment.
type ProviderSelections struct {
	Development ProviderSelection `json:"development"`
	Test        ProviderSelection `json:"test"`
	Production  ProviderSelection `json:"production"`
}

// ProviderSelection is an adapter and service target pair.
type ProviderSelection struct {
	Adapter string `json:"adapter"`
	Target  string `json:"target"`
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
	// FileGenerated marks a target the build produces. It has no base digest:
	// the snapshot excludes generated outputs, so there are no canonical bytes
	// to compare against and no modification to track.
	FileGenerated FileState = "generated"
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
	// FileClassGenerated marks a file the build produces rather than the
	// registry distributes: built CSS, aggregated scripts, generated tables.
	// It carries no payload digest — the snapshot excludes generated outputs on
	// purpose (including one would let writing it invalidate the lock), so a
	// generated file can never be verified or installed as source.
	FileClassGenerated FileClass = "generated"
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
//
// Most slots are ADDITIVE: the shell renders every active contribution at the
// marker and nothing when there is none. The two mount slots are EXCLUSIVE:
// the shell renders its own neutral container when no contribution is active
// and steps aside entirely when one is, because the contributing adapter owns
// the whole element — its id, its classes, its data attributes. A mount cannot
// be additive: two elements with the same id is not a shell, it is a bug.
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
	// The exclusive mount slots. Named after the place in the shell, never
	// after what fills it: an identity provider's org switcher and account
	// widget are the only things that have ever wanted them, but the shell
	// only knows it has a box for one of each.
	ShellSlotOrgSwitcher ShellSlot = "org-switcher"
	ShellSlotUserButton  ShellSlot = "user-button"
)

// ExclusiveShellSlots are the mount slots: at most one active contribution,
// and the shell's own container is the fallback rather than a wrapper.
//
// The generator enforces this over the INSTALLED UNION, not per environment,
// which is stricter than the runtime needs: two identity adapters that never
// run in the same environment still cannot both own a mount, so a development
// org-switcher stub beside the hosted one is refused today. That is a
// deliberate deferral, not a claim that the union is the right boundary — the
// per-environment form would resolve it against the same selection
// providerActive already uses, and the day someone wants that stub is the day
// to write it.
var ExclusiveShellSlots = []ShellSlot{ShellSlotOrgSwitcher, ShellSlotUserButton}

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

// GalleryFamilies is every reference family in the order the gallery presents
// them, mirroring ui.GalleryFamilies. The visual surface matrix walks this
// slice, so the order a reader browses families in and the order their
// baselines are compared in cannot diverge.
var GalleryFamilies = []GalleryFamily{
	GalleryFoundations, GalleryActions, GalleryForms, GalleryNavigation,
	GalleryFeedback, GalleryOverlays, GalleryData, GalleryCommunication,
	GalleryLayout, GalleryAdvanced,
}

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
	ID           string               `json:"id"`
	Kind         ModuleKind           `json:"kind"`
	Name         string               `json:"name"`
	Revision     int                  `json:"revision"`
	Contract     int                  `json:"contract"`
	Title        string               `json:"title"`
	Description  string               `json:"description"`
	Requires     []Requirement        `json:"requires"`
	Dependencies Dependencies         `json:"dependencies"`
	Files        []ManifestFile       `json:"files"`
	Claims       NamespaceClaims      `json:"claims"`
	Runtime      RuntimeContributions `json:"runtime"`
	// Vendors records the provenance of every third-party file this module
	// commits into the tree. Declared per module rather than centrally so that
	// removing a module removes its vendored bytes with it.
	Vendors     []VendorArtifact      `json:"vendors,omitempty"`
	Migrations  []ManifestMigration   `json:"migrations"`
	Environment []EnvironmentVariable `json:"environment"`
	// Locales maps a locale code to the keys this module owns in that locale.
	// A module's own translations travel with it, so installing a page installs
	// its strings and removing it removes them. Ownership is exclusive: two
	// modules declaring one key would make the rendered string depend on which
	// other modules happen to be installed.
	Locales map[string]map[string]string `json:"locales,omitempty"`
	// OpenAPI is this module's slice of the /api/v1 contract, when it serves any.
	OpenAPI *OpenAPIContribution `json:"openapi,omitempty"`
	// Personas are the synthetic actors fixtures and e2e share: one declaration
	// feeds the session-cookie helper and the parity check that keeps the
	// seeded rows and the specs agreeing about who an actor is.
	Personas      []PersonaContribution `json:"personas,omitempty"`
	Docs          []DocumentationRef    `json:"docs"`
	Tests         TestMetadata          `json:"tests"`
	Data          []DataDeclaration     `json:"data"`
	RemovalPolicy RemovalPolicy         `json:"removal_policy"`
	TestOnly      bool                  `json:"test_only,omitempty"`
}

// Requirement declares a dependency and the inclusive provider contract range
// consumed by this module.
type Requirement struct {
	ID       string         `json:"id"`
	Contract ContractBounds `json:"contract"`
}

type ContractBounds struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type Dependencies struct {
	Go []GoDependency `json:"go"`
	// GoTools names the module-path tools the project runs through `go tool`
	// (templ, sqlc, goose, ...). Sync keeps the derivative go.mod tool block
	// in step with the union, so `go tool` needs no network and no guesswork.
	GoTools    []string              `json:"go_tools,omitempty"`
	Tools      []ToolArtifact        `json:"tools"`
	Containers []ContainerDependency `json:"containers"`
}

type GoDependency struct {
	Module  string `json:"module"`
	Version string `json:"version"`
}

type ToolArtifact struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Format      string `json:"format"`
	BinaryPath  string `json:"binary_path"`
	InstallPath string `json:"install_path"`
}

type ContainerDependency struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

// ManifestFile maps one verified registry payload to one project-owned target.
type ManifestFile struct {
	Source        string    `json:"source"`
	Target        string    `json:"target"`
	Class         FileClass `json:"class"`
	SHA256        string    `json:"sha256"`
	RewriteModule bool      `json:"rewrite_module"`
	Contract      bool      `json:"contract"`
	// SelfHost marks a payload that asserts something about the registry's own
	// repository rather than about the source it distributes: the committed
	// snapshot signature, the example and external fixtures, the CI workflows,
	// the vendored bytes, the repository-wide ownership sweep. A project only
	// installs it when the project IS that repository — when its go.mod module
	// path equals the owning registry's canonical_module, the same
	// discriminator rewrite_module keys off. Everywhere else the payload is
	// declared, verified and skipped: a derivative must never receive an
	// assertion about artifacts it neither has nor should have.
	SelfHost bool `json:"self_host,omitempty"`
}

// NamespaceClaims contains collision-checked names. Empty claims encode as {}.

// NamespaceClaims contains collision-checked names. Empty claims encode as {}.
type NamespaceClaims struct {
	Packages      []string `json:"packages,omitempty"`
	Routes        []string `json:"routes,omitempty"`
	Jobs          []string `json:"jobs,omitempty"`
	Environment   []string `json:"environment,omitempty"`
	I18n          []string `json:"i18n,omitempty"`
	Queries       []string `json:"queries,omitempty"`
	OpenAPI       []string `json:"openapi,omitempty"`
	ContentTypes  []string `json:"content_types,omitempty"`
	UI            []string `json:"ui,omitempty"`
	Assets        []string `json:"assets,omitempty"`
	Data          []string `json:"data,omitempty"`
	ProviderSlots []string `json:"provider_slots,omitempty"`
	Provisioners  []string `json:"provisioners,omitempty"`
	DatabaseOps   []string `json:"database_ops,omitempty"`
	CLI           []string `json:"cli,omitempty"`
	Deploy        []string `json:"deploy,omitempty"`
}

// RuntimeContributions is the typed, data-only runtime declaration. Empty
// contributions encode as {}.
type RuntimeContributions struct {
	System        *SystemContribution        `json:"system,omitempty"`
	ProviderSlots []ProviderSlotContribution `json:"provider_slots,omitempty"`
	Provisioners  []ProvisionerContribution  `json:"provisioners,omitempty"`
	DatabaseOps   []DatabaseOpsContribution  `json:"database_ops,omitempty"`
	Deploy        []DeployContribution       `json:"deploy,omitempty"`
	CLI           []CLIContribution          `json:"cli,omitempty"`
	Routes        []RouteContribution        `json:"routes,omitempty"`
	Jobs          []JobContribution          `json:"jobs,omitempty"`
	Janitors      []JanitorContribution      `json:"janitors,omitempty"`
	Queries       []QueryContribution        `json:"queries,omitempty"`
	ContentTypes  []ContentTypeContribution  `json:"content_types,omitempty"`
	Navigation    []NavigationContribution   `json:"navigation,omitempty"`
	Slots         []SlotContribution         `json:"slots,omitempty"`
	CSP           []CSPContribution          `json:"csp,omitempty"`
	UI            []UIContribution           `json:"ui,omitempty"`
	Assets        []AssetContribution        `json:"assets,omitempty"`
	Scenarios     []ScenarioContribution     `json:"scenarios,omitempty"`
	Visual        []VisualContribution       `json:"visual,omitempty"`
}

type ProvisionerContribution struct {
	ID          string `json:"id"`
	Package     string `json:"package"`
	Constructor string `json:"constructor"`
}

type DatabaseOpsContribution struct {
	ID          string `json:"id"`
	Package     string `json:"package"`
	Constructor string `json:"constructor"`
}

type DeployContribution struct {
	ID          string `json:"id"`
	Package     string `json:"package"`
	Constructor string `json:"constructor"`
}

// CLIContribution declares one project-local ggg command. Installation
// executes nothing: the declared handler runs only when the operator invokes
// the command by name, and it reaches the project exclusively through the
// gggcli controller. A contributed name must be claimed under claims.cli; the
// reserved built-in names are refused when the command registry is assembled.
type CLIContribution struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Package string `json:"package"`
	Handler string `json:"handler"`
}

// CapabilityContribution names one typed value exported by a provider slot.
type CapabilityContribution struct {
	Capability string `json:"capability"`
	Type       string `json:"type"`
}

// ProviderSlotContribution declares a constructor-free provider seam.
type ProviderSlotContribution struct {
	ID           string                   `json:"id"`
	Critical     bool                     `json:"critical"`
	Capabilities []CapabilityContribution `json:"capabilities"`
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

// AdapterContribution marks a system module as an implementation of a
// provider slot and advertises its selectable service targets.
type AdapterContribution struct {
	Slot    string          `json:"slot"`
	Targets []ServiceTarget `json:"targets"`
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
	TypeImports []string             `json:"type_imports,omitempty"`
	Start       bool                 `json:"start"`
	Stop        bool                 `json:"stop"`
	Health      bool                 `json:"health,omitempty"`
	Adapter     *AdapterContribution `json:"adapter,omitempty"`
}

// ServiceTarget is the provider-facing endpoint choice exposed by an adapter.
type ServiceTarget struct {
	ID               string        `json:"id"`
	Title            string        `json:"title"`
	Mode             string        `json:"mode"`
	Environments     []string      `json:"environments"`
	Automation       string        `json:"automation"`
	Provisioner      string        `json:"provisioner,omitempty"`
	DatabaseOperator string        `json:"database_operator,omitempty"`
	DocsURL          string        `json:"docs_url"`
	ConsoleURL       string        `json:"console_url,omitempty"`
	Inputs           []TargetInput `json:"inputs"`
	LocalService     *LocalService `json:"local_service,omitempty"`
}

type TargetInput struct {
	Key      string   `json:"key"`
	EnvKey   string   `json:"env_key,omitempty"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Secret   bool     `json:"secret"`
	Enum     []string `json:"enum,omitempty"`
}

type LocalService struct {
	Container   string               `json:"container"`
	Ports       []LocalServicePort   `json:"ports"`
	Environment []LocalServiceEnv    `json:"environment"`
	Volumes     []LocalServiceVolume `json:"volumes"`
	Health      LocalServiceHealth   `json:"health"`
}

type LocalServicePort struct {
	Name        string `json:"name"`
	Container   int    `json:"container"`
	DefaultHost int    `json:"default_host"`
}

type LocalServiceEnv struct {
	Key     string `json:"key"`
	Value   string `json:"value,omitempty"`
	FromKey string `json:"from_key,omitempty"`
}

type LocalServiceVolume struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type LocalServiceHealth struct {
	Kind string `json:"kind"`
	Port int    `json:"port"`
	Path string `json:"path,omitempty"`
	// Command overrides the generic nc/wget probe with the image's own
	// healthcheck tool (pg_isready for postgres, a vendor health endpoint for
	// object stores). Declared per target because images differ in what they
	// ship; the generator never guesses.
	Command string `json:"command,omitempty"`
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

// GlobalRequestBodyLimit is the cap the server applies to every route. A route
// may declare a tighter MaxBodyBytes; it can never raise this. Validation
// refuses a declared value at or above it, because such a value reads like a
// limit and applies none.
//
// It is duplicated in internal/web as globalMaxBodyBytes rather than imported:
// the runtime must not depend on the CLI package it is generated by.
// TestGlobalBodyLimitMatchesTheValidator holds the two together.
const GlobalRequestBodyLimit int64 = 10 << 20

// RoutePolicy is the closed security and transport policy for a route.
type RoutePolicy struct {
	CSRFExempt        bool   `json:"csrf_exempt"`
	CSRFReason        string `json:"csrf_reason"`
	RateExempt        bool   `json:"rate_exempt"`
	MaintenanceExempt bool   `json:"maintenance_exempt"`
	// MaxBodyBytes caps this route's request body. 0 means the global cap;
	// anything else must be smaller than GlobalRequestBodyLimit.
	MaxBodyBytes int64 `json:"max_body_bytes"`
	Idempotent   bool  `json:"idempotent"`
	AdminWrite   bool  `json:"admin_write"`
}

// JobContribution declares one typed generated job definition.
type JobContribution struct {
	Kind        string `json:"kind"`
	Package     string `json:"package"`
	Handler     string `json:"handler"`
	Schedulable bool   `json:"schedulable,omitempty"`
	MaxAttempts int    `json:"max_attempts,omitempty"`
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

// PersonaContribution is one synthetic actor. The session cookie format is
// derived from these fields, so a spec and a fixture cannot disagree about who
// an actor is.
type PersonaContribution struct {
	ID   string `json:"id"`
	User string `json:"user"`
	Org  string `json:"org"`
	Role string `json:"role"`
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

// CSPDirective is the closed set of Content-Security-Policy directives an
// installed module may add sources to.
//
// The set is deliberately not "every directive in the policy". Three of the
// policy's ten are the framework's posture rather than a list anybody extends:
//
//   - script-src is 'self' and nothing else, which is what makes the vendored
//     files the only script that can run. Every third-party byte this product
//     ships is committed and self-served; an adapter that wants a CDN wants
//     the vendoring rule repealed, and that is not a contribution.
//   - default-src is the fallback the others narrow. Widening it widens
//     everything that has no directive of its own.
//   - base-uri, form-action and frame-ancestors are navigation and embedding
//     controls. Adding a source there changes where the document can be framed
//     or where its forms can post, which no browser SDK needs and which is the
//     shape of a real attack.
//
// What remains is the set a hosted widget legitimately needs: somewhere to
// call, somewhere to load an avatar from, and — for one vendor's session
// handshake — a blob: worker.
type CSPDirective string

const (
	CSPConnectSrc CSPDirective = "connect-src"
	CSPImgSrc     CSPDirective = "img-src"
	CSPWorkerSrc  CSPDirective = "worker-src"
	CSPFontSrc    CSPDirective = "font-src"
	CSPStyleSrc   CSPDirective = "style-src"
	CSPMediaSrc   CSPDirective = "media-src"
	CSPFrameSrc   CSPDirective = "frame-src"
)

// ContributableCSPDirectives is that set, in the order the header renders them.
var ContributableCSPDirectives = []CSPDirective{
	CSPStyleSrc, CSPImgSrc, CSPFontSrc, CSPConnectSrc, CSPWorkerSrc,
	CSPMediaSrc, CSPFrameSrc,
}

// CSPContribution declares a function that returns Content-Security-Policy
// sources for the directives the module names.
//
// It is shaped like SlotContribution on purpose: a package, a symbol, and the
// contributing module's own non-secret configuration as the only input. CSP is
// a per-deployment decision rather than a per-request one, so the function
// takes no context and the composed header is computed once.
//
// Directives is the grant. A source returned for a directive the manifest does
// not name is a refusal, so reading the manifest tells you the whole blast
// radius of the contribution without reading its code.
type CSPContribution struct {
	ID         string         `json:"id"`
	Directives []CSPDirective `json:"directives"`
	Package    string         `json:"package"`
	Sources    string         `json:"sources"`
}

// UIContribution is the generated component metadata supplied by a UI item.
type UIContribution struct {
	Name           string        `json:"name"`
	Family         GalleryFamily `json:"family"`
	Engine         string        `json:"engine,omitempty"`
	Alpine         string        `json:"alpine,omitempty"`
	Vendor         string        `json:"vendor,omitempty"`
	NativeFallback string        `json:"native_fallback,omitempty"`
	// Signature is the renderer's exact declaration, e.g.
	// "templ Button(o ButtonOpts)". It is declared rather than read from source
	// because GenerateAll is a pure function of the manifest graph; a drift test
	// asserts it still matches the code, so a declared signature cannot quietly
	// describe a renderer that changed shape.
	Signature string `json:"signature,omitempty"`
	// Summary is one sentence on what the component is for, taken from the
	// renderer's own doc comment so the reference and the code cannot disagree.
	Summary string `json:"summary,omitempty"`
	// Guidance is the reasoning a reader needs before choosing this component:
	// when it applies, what it refuses to do, and the accessibility decision
	// behind that refusal.
	Guidance string `json:"guidance,omitempty"`
	// Keyboard is the interaction contract for components that implement one.
	// Absent means the component adds no key handling of its own, which is a
	// different statement from "unknown".
	Keyboard string `json:"keyboard,omitempty"`
	// States are the rendering states this component actually has. Declaring
	// them lets the reference show every one and lets the visual matrix cover
	// them, instead of both guessing from a fixed list that fits nothing.
	States []string `json:"states,omitempty"`
}

// ScenarioContribution declares one realistic product surface the dev catalog
// composes. A scenario is a deliberate choice of which components appear
// together, so it is declared rather than discovered — discovering it from
// installed modules would produce a component index, which the gallery already
// is. It lives in the manifest so the Go descriptor table and the visual
// surface matrix are rendered from one statement instead of two lists that can
// disagree about which surfaces exist.
type ScenarioContribution struct {
	// Slug is the URL segment and the visual baseline identity, so renaming one
	// is a visible change rather than a silent baseline reset.
	Slug string `json:"slug"`
	// Title and Summary describe the surface a reader is looking at.
	Title   string `json:"title"`
	Summary string `json:"summary"`
	// Layout is the real shell this scenario renders inside. It is the same
	// string Page.Layout takes, so a scenario cannot name a shell the renderer
	// does not have.
	Layout string `json:"layout"`
	// Surfaces names the components this scenario exists to exercise. It is what
	// the visual matrix and the accessibility sweep read to know their coverage.
	Surfaces []string `json:"surfaces"`
	// States are the query values this scenario accepts. A state absent here is
	// refused rather than silently rendering the default, because a URL that
	// looks like it selected something and did not is worse than a 404.
	States []string `json:"states"`
}

// VisualContribution declares one page surface in the visual comparison
// matrix. It is a separate list from Routes rather than a field on one because
// a compared surface is not one-to-one with a route: /admin is compared twice,
// once as an administrator and once as the 403 a non-admin sees, and the 404
// baseline has no route at all.
type VisualContribution struct {
	// ID is the baseline file stem. Changing it orphans committed screenshots,
	// so the existing names are carried verbatim rather than regularised.
	ID string `json:"id"`
	// Path is the URL to visit, query included when a state is part of the
	// surface.
	Path string `json:"path"`
	// Persona is the actor to authenticate as, empty for anonymous. It must name
	// a declared persona so a baseline cannot be captured as a user the fixtures
	// never seed.
	Persona string `json:"persona,omitempty"`
	// FullPage compares the whole document rather than the fold. A reference
	// sheet needs it; a product page does not, and comparing its full height
	// would make every added row a diff.
	FullPage bool `json:"full_page,omitempty"`
	// Viewports are the widths this surface is compared at. Declared rather than
	// defaulted: a surface silently gaining a viewport would fail on a baseline
	// nobody captured.
	Viewports []string `json:"viewports"`
	// Masks are selectors covering values that legitimately differ between runs.
	// Each one hides pixels from comparison, so an over-broad mask is a
	// regression nothing can see — they are declared per surface for that
	// reason.
	Masks []string `json:"masks,omitempty"`
}

// AssetContribution declares an ordered same-origin static asset.
type AssetContribution struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Kind      AssetKind `json:"kind"`
	Engine    string    `json:"engine,omitempty"`
	Integrity string    `json:"integrity,omitempty"`
	ESM       bool      `json:"esm,omitempty"`
	Before    []string  `json:"before,omitempty"`
	After     []string  `json:"after,omitempty"`
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
	Key                string   `json:"key"`
	Field              string   `json:"field"`
	Type               EnvType  `json:"type"`
	Description        string   `json:"description"`
	Default            string   `json:"default,omitempty"`
	Example            string   `json:"example,omitempty"`
	Min                *int     `json:"min,omitempty"`
	Max                *int     `json:"max,omitempty"`
	Enum               []string `json:"enum,omitempty"`
	TrimSlash          bool     `json:"trim_slash,omitempty"`
	Required           bool     `json:"required,omitempty"`
	ProductionRequired bool     `json:"production_required,omitempty"`
	// RefusedInProduction makes a true value a boot refusal under
	// APP_ENV=production. It exists so an escape hatch that only makes sense
	// off production is refused by the module that ships it, rather than by
	// hand-written code in the config seam that outlives the module's removal.
	RefusedInProduction bool `json:"refused_in_production,omitempty"`
	// Derivation fills the key when the operator supplies nothing. It leaves
	// with the declaring module, which is the whole point: a derivation
	// written into the config seam by hand survives removing the adapter that
	// gave the key meaning.
	Derivation *EnvironmentDerivation `json:"derivation,omitempty"`
	Secret     bool                   `json:"secret,omitempty"`
	Targets    []string               `json:"targets,omitempty"`
}

// EnvironmentDerivation names a pure function that computes one declared value
// from other declared values. The function lives in a leaf package the
// declaring module owns and imports nothing that imports config, because the
// generated loader calls it: a derivation expressed as manifest data instead
// would be a template language, and this repository would rather compile three
// lines of Go.
//
// Inputs are declared keys, resolved to their generated fields in order, so
// the call is type-checked by the compiler rather than by the generator.
type EnvironmentDerivation struct {
	Package  string   `json:"package"`
	Function string   `json:"function"`
	Inputs   []string `json:"inputs"`
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
	Schema int `json:"schema"`
	// EngineContract records the engine contract of the binary that wrote this
	// lock. Schema versions the file format; this versions the behavior the
	// resolved state assumes of its reader. A reader refuses a lock whose
	// contract is newer than its own compiled-in EngineContract.
	//
	// Every lock MarshalLock writes records it, so the key set of a written
	// lock is still fixed. It is omitempty only so a lock written before the
	// guard existed reads as contract 0 — the oldest there is, which a newer
	// binary must accept silently. Refusing it for a missing key would be the
	// same unactionable engine error the guard exists to replace.
	EngineContract int              `json:"engine_contract,omitempty"`
	RegistryCommit string           `json:"registry_commit"`
	Registries     []LockedRegistry `json:"registries"`
	Snapshots      []LockedSnapshot `json:"snapshots"`
	Order          []string         `json:"order"`
	RuntimeOrders  RuntimeOrders    `json:"runtime_orders"`
	// Providers records the exact adapter@target selected for every slot and
	// environment. Keeping this in the lock makes generated consumers (health,
	// navigation, and shell registries) deterministic without consulting mutable
	// intent at generation time.
	Providers map[string]ProviderSelections `json:"providers,omitempty"`
	// Ports records the project's declared host-port overrides for the same
	// reason: the Compose generator reads its inputs from the resolved lock,
	// never from mutable intent, so a generated Compose file and the lock that
	// produced it cannot disagree about where a stack publishes.
	Ports        map[string]PortOverrides `json:"ports,omitempty"`
	GoTools      []string                 `json:"go_tools,omitempty"`
	Dependencies []LockedDependency       `json:"dependencies"`
	Modules      []LockedModule           `json:"modules"`
}

type LockedRegistry struct {
	Namespace       string `json:"namespace"`
	Source          string `json:"source"`
	RequestedRef    string `json:"requested_ref"`
	CanonicalModule string `json:"canonical_module"`
	KeyFingerprint  string `json:"key_fingerprint"`
}

type LockedSnapshot struct {
	Namespace      string `json:"namespace"`
	Commit         string `json:"commit"`
	SnapshotSHA256 string `json:"snapshot_sha256"`
	CacheKey       string `json:"cache_key"`
}

type RuntimeOrders struct {
	Development []string `json:"development"`
	Test        []string `json:"test"`
	Production  []string `json:"production"`
}

type LockedDependency struct {
	Module          string   `json:"module"`
	ManagedVersion  string   `json:"managed_version"`
	Owners          []string `json:"owners"`
	Preexisting     bool     `json:"preexisting"`
	BaselineVersion string   `json:"baseline_version"`
}

// LockedModule is one installed module and its canonical manifest snapshot.
type LockedModule struct {
	ID                string            `json:"id"`
	Revision          int               `json:"revision"`
	Contract          int               `json:"contract"`
	RegistryNamespace string            `json:"registry_namespace"`
	SourceCommit      string            `json:"source_commit"`
	SnapshotSHA256    string            `json:"snapshot_sha256"`
	Reason            string            `json:"reason"`
	RequiredBy        []string          `json:"required_by"`
	Manifest          Manifest          `json:"manifest"`
	Files             []LockedFile      `json:"files"`
	Migrations        []LockedMigration `json:"migrations"`
	Pending           *PendingUpdate    `json:"pending,omitempty"`
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
