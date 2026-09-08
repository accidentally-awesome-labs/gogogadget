package modkit

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractToolRejectsUnsafeArchiveAndInstallPaths(t *testing.T) {
	data := []byte("tool")
	hash := sha256.Sum256(data)
	base := ToolArtifact{OS: "darwin", Arch: "arm64", URL: "https://example.test/tool", SHA256: hex.EncodeToString(hash[:]), Format: "raw", BinaryPath: "tool", InstallPath: "bin/tool"}
	if err := ExtractTool(data, base, t.TempDir()); err != nil {
		t.Fatalf("raw extraction: %v", err)
	}
	for _, artifact := range []ToolArtifact{
		{URL: "http://example.test/tool", SHA256: base.SHA256, Format: "raw", BinaryPath: "tool", InstallPath: "bin/tool"},
		{URL: base.URL, SHA256: base.SHA256, Format: "raw", BinaryPath: "../tool", InstallPath: "bin/tool"},
		{URL: base.URL, SHA256: base.SHA256, Format: "raw", BinaryPath: "tool", InstallPath: "../tool"},
	} {
		if err := ExtractTool(data, artifact, t.TempDir()); err == nil {
			t.Fatalf("unsafe artifact accepted: %#v", artifact)
		}
	}
}

func TestExtractToolRejectsZipTraversalAndUnlistedExecutables(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, item := range []struct {
		name string
		mode uint32
	}{{"tool", 0o755}, {"extra", 0o755}} {
		header := &zip.FileHeader{Name: item.name}
		header.SetMode(os.FileMode(item.mode))
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(item.name))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	artifact := ToolArtifact{OS: "darwin", Arch: "arm64", URL: "https://example.test/tool.zip", SHA256: hex.EncodeToString(sum[:]), Format: "zip", BinaryPath: "tool", InstallPath: "bin/tool"}
	if err := ExtractTool(archive.Bytes(), artifact, t.TempDir()); err == nil {
		t.Fatal("zip with undeclared executable accepted")
	}
}

func TestValidateDeclaredImportsScansGeneratedGo(t *testing.T) {
	generated := []string{"package generated\nimport \"example.com/undeclared/client\"\n"}
	if err := ValidateDeclaredImports(nil, generated, nil); err == nil || !strings.Contains(err.Error(), "undeclared") {
		t.Fatalf("generated undeclared import error = %v", err)
	}
}

func TestReconcileManagedDependenciesPreservesUserChange(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n\ngo 1.26\n\nrequire example.com/provider v1.4.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte("sum-before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous := []LockedDependency{{Module: "example.com/provider", ManagedVersion: "v1.2.0", Owners: []string{"ggg/system/provider"}}}
	if _, err := ReconcileManagedDependencies(context.Background(), root, previous, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "example.com/provider v1.4.0") {
		t.Fatalf("user requirement changed: %s", data)
	}
}

func TestRuntimeOrdersForAddsCapabilityEdges(t *testing.T) {
	provider := Manifest{ID: "ggg/system/z-provider", Kind: ModuleSystem, Runtime: RuntimeContributions{System: &SystemContribution{Adapter: &AdapterContribution{Slot: "ggg/mail", Targets: []ServiceTarget{{ID: "local", Mode: "development", Environments: []string{"development"}, Automation: "manual", Title: "local", DocsURL: "https://example.test"}}}, Provides: []RuntimeProvide{{Capability: "mail.sender", Type: "mail.Sender"}}}}}
	consumer := Manifest{ID: "ggg/system/a-consumer", Kind: ModuleSystem, Runtime: RuntimeContributions{System: &SystemContribution{Needs: []RuntimeNeed{{Capability: "mail.sender", Field: "Sender", Type: "mail.Sender"}}}}}
	orders, err := RuntimeOrdersFor(context.Background(), []Manifest{consumer, provider}, Project{Providers: map[string]ProviderSelections{"ggg/mail": {Development: ProviderSelection{Adapter: provider.ID, Target: "local"}, Test: ProviderSelection{Adapter: provider.ID, Target: "local"}, Production: ProviderSelection{Adapter: provider.ID, Target: "local"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if indexOf(orders.Development, provider.ID) > indexOf(orders.Development, consumer.ID) {
		t.Fatalf("provider ordered after consumer: %v", orders.Development)
	}
}

func TestAdapterEnvironmentTargetsGateRequiredKeys(t *testing.T) {
	local := Manifest{
		ID: "ggg/system/mail-local", Kind: ModuleSystem,
		Environment: []EnvironmentVariable{{Key: "LOCAL_TOKEN", Field: "LocalToken", Type: EnvString, Required: true}},
		Runtime: RuntimeContributions{System: &SystemContribution{
			Adapter: &AdapterContribution{Slot: "ggg/mail", Targets: []ServiceTarget{{ID: "filesystem", Title: "Filesystem", Mode: "development", Environments: []string{"development"}, Automation: "manual", DocsURL: "https://example.test"}}},
		}},
	}
	managed := Manifest{
		ID: "ggg/system/mail-managed", Kind: ModuleSystem,
		Environment: []EnvironmentVariable{{Key: "MANAGED_TOKEN", Field: "ManagedToken", Type: EnvString, Required: true}},
		Runtime: RuntimeContributions{System: &SystemContribution{
			Adapter: &AdapterContribution{Slot: "ggg/mail", Targets: []ServiceTarget{{ID: "resend", Title: "Resend", Mode: "managed", Environments: []string{"production"}, Automation: "configure", DocsURL: "https://example.test"}}},
		}},
	}
	lock := Lock{Schema: 2, Providers: map[string]ProviderSelections{
		"ggg/mail": {
			Development: ProviderSelection{Adapter: local.ID, Target: "filesystem"},
			Test:        ProviderSelection{Adapter: local.ID, Target: "filesystem"},
			Production:  ProviderSelection{Adapter: managed.ID, Target: "resend"},
		},
	}}
	declarations, err := declaredEnvironment(lock, []Manifest{local, managed})
	if err != nil {
		t.Fatal(err)
	}
	if got := requiredExpression(declarations[0], lock); !strings.Contains(got, `cfg.Env == "development"`) || strings.Contains(got, `cfg.Env == "production"`) {
		t.Fatalf("local required expression = %q", got)
	}
	if got := requiredExpression(declarations[1], lock); !strings.Contains(got, `cfg.Env == "production"`) || strings.Contains(got, `cfg.Env == "development"`) {
		t.Fatalf("managed required expression = %q", got)
	}
	out, err := emitConfigRegistry(context.Background(), "example.com/app", lock, []Manifest{local, managed})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Content, `if true && cfg.LocalToken`) || strings.Contains(out.Content, `if true && cfg.ManagedToken`) {
		t.Fatalf("adapter required keys are unconditional:\n%s", out.Content)
	}
}

func TestProviderSelectionAndClaimRefusals(t *testing.T) {
	seam := Manifest{ID: "ggg/system/mail", Kind: ModuleSystem, Runtime: RuntimeContributions{
		ProviderSlots: []ProviderSlotContribution{{ID: "ggg/mail", Capabilities: []CapabilityContribution{{Capability: "mail.sender", Type: "mail.Sender"}}}},
	}}
	adapter := func(id, slot, target, mode string, environments []string) Manifest {
		return Manifest{ID: id, Kind: ModuleSystem, Runtime: RuntimeContributions{System: &SystemContribution{
			Adapter:  &AdapterContribution{Slot: slot, Targets: []ServiceTarget{{ID: target, Title: target, Mode: mode, Environments: environments, Automation: "manual", DocsURL: "https://example.test"}}},
			Provides: []RuntimeProvide{{Field: "Sender", Capability: "mail.sender", Type: "mail.Sender"}},
		}}}
	}
	local := adapter("ggg/system/mail-local", "ggg/mail", "filesystem", "development", []string{"development", "test"})
	remote := adapter("ggg/system/mail-remote", "ggg/mail", "resend", "managed", []string{"production"})
	project := Project{Schema: 2, Modules: []string{seam.ID}, Providers: map[string]ProviderSelections{"ggg/mail": {
		Development: ProviderSelection{Adapter: local.ID, Target: "filesystem"},
		Test:        ProviderSelection{Adapter: local.ID, Target: "filesystem"},
		Production:  ProviderSelection{Adapter: remote.ID, Target: "resend"},
	}}}
	catalog := Catalog{Modules: []Manifest{seam, local, remote}}
	if _, err := resolveSelectedGraph(t.Context(), project, catalog); err != nil {
		t.Fatalf("valid provider fixture refused: %v", err)
	}
	for name, mutate := range map[string]func(*Project){
		"wrong slot": func(p *Project) {
			choices := p.Providers["ggg/mail"]
			choices.Development.Adapter = "ggg/system/other"
			p.Providers["ggg/mail"] = choices
		},
		"missing target": func(p *Project) {
			choices := p.Providers["ggg/mail"]
			choices.Development.Target = "missing"
			p.Providers["ggg/mail"] = choices
		},
		"development target in production": func(p *Project) {
			choices := p.Providers["ggg/mail"]
			choices.Production = ProviderSelection{Adapter: local.ID, Target: "filesystem"}
			p.Providers["ggg/mail"] = choices
		},
		"explicit unused adapter": func(p *Project) { p.Modules = append(p.Modules, "ggg/system/unused") },
	} {
		t.Run(name, func(t *testing.T) {
			copy := project
			copy.Providers = map[string]ProviderSelections{"ggg/mail": project.Providers["ggg/mail"]}
			copy.Modules = append([]string{}, project.Modules...)
			mutate(&copy)
			if _, err := resolveSelectedGraph(t.Context(), copy, catalog); err == nil {
				t.Fatalf("refusal %q was accepted", name)
			}
		})
	}
	duplicate := seam
	duplicate.ID = "ggg/system/mail-other"
	duplicate.Runtime.ProviderSlots = append([]ProviderSlotContribution{}, seam.Runtime.ProviderSlots...)
	if err := preflightNamespaces(t.Context(), []Manifest{seam, duplicate}); err == nil {
		t.Fatal("duplicate non-adapter provider slot accepted")
	}
	claimed := local
	claimed.Runtime.Provisioners = []ProvisionerContribution{{ID: "mail.provision", Package: "internal/mail", Constructor: "New"}}
	claimed.Runtime.System.Adapter.Targets[0].Automation = "provision"
	claimed.Runtime.System.Adapter.Targets[0].Provisioner = "mail.missing"
	if err := preflightNamespaces(t.Context(), []Manifest{seam, claimed}); err == nil {
		t.Fatal("unclaimed provisioner accepted")
	}
}

// A seam reachable ONLY through a selected adapter's `requires` declares its
// slot as loudly as one named in the profile, and an installed seam with no
// adapter selected boots a nil capability.
//
// The resolver used to freeze the slot set before expanding what the adapters
// and the deployment module require, ninety lines apart, so both of the
// refusals below were defeated by that one ordering fact — and both ship a
// project rather than fail a test. Each case has a control that was already
// caught, so the assertion is about the POPULATION and not the pattern.
func TestProviderClosureReachesThroughSelectedAdapters(t *testing.T) {
	slotSeam := func(id, slot string) Manifest {
		return Manifest{ID: id, Kind: ModuleSystem, Contract: 1, Runtime: RuntimeContributions{
			ProviderSlots: []ProviderSlotContribution{{ID: slot, Capabilities: []CapabilityContribution{{Capability: slot + ".thing", Type: "any"}}}},
		}}
	}
	requires := func(ids ...string) []Requirement {
		out := make([]Requirement, 0, len(ids))
		for _, id := range ids {
			out = append(out, Requirement{ID: id, Contract: ContractBounds{Min: 1, Max: 1}})
		}
		return out
	}
	adapter := func(id, slot string, pulls ...string) Manifest {
		return Manifest{ID: id, Kind: ModuleSystem, Contract: 1, Requires: requires(pulls...), Runtime: RuntimeContributions{System: &SystemContribution{
			Adapter: &AdapterContribution{Slot: slot, Targets: []ServiceTarget{{
				ID: "t", Title: "T", Mode: "managed", Environments: []string{"development", "test", "production"},
				Automation: "manual", DocsURL: "https://example.test",
			}}},
		}}}
	}
	deployModule := func(id string, pulls ...string) Manifest {
		return Manifest{ID: id, Kind: ModuleSystem, Contract: 1, Requires: requires(pulls...), Runtime: RuntimeContributions{
			System: &SystemContribution{Package: "internal/deploy", Constructor: "New"},
			Deploy: []DeployContribution{{ID: id + ".target", Package: "internal/deploy", Constructor: "New"}},
		}}
	}
	selections := func(adapterID string) ProviderSelections {
		choice := ProviderSelection{Adapter: adapterID, Target: "t"}
		return ProviderSelections{Development: choice, Test: choice, Production: choice}
	}

	t.Run("a slot declared only through a selected adapter", func(t *testing.T) {
		seamA := slotSeam("x/system/seam-a", "x/slot-a")
		seamB := slotSeam("x/system/seam-b", "x/slot-b")
		smuggler := adapter("x/system/adapter-a", "x/slot-a", seamB.ID)
		root := Manifest{ID: "x/workflow/root", Kind: ModuleWorkflow, Contract: 1, Requires: requires(seamA.ID)}
		catalog := Catalog{Modules: []Manifest{seamA, seamB, smuggler, root}}
		project := Project{Schema: 2, Modules: []string{root.ID}, Providers: map[string]ProviderSelections{
			"x/slot-a": selections(smuggler.ID),
		}}
		_, err := resolveSelectedGraph(t.Context(), project, catalog)
		if err == nil || !strings.Contains(err.Error(), "missing [x/slot-b]") {
			t.Fatalf("error = %v, want the slot the adapter's requires reached named as missing", err)
		}

		// Control: the same seam in the base closure, which the frozen slot
		// set did catch. Same refusal, so the population is the difference.
		base := root
		base.Requires = requires(seamA.ID, seamB.ID)
		control := Catalog{Modules: []Manifest{seamA, seamB, adapter("x/system/adapter-a", "x/slot-a"), base}}
		_, err = resolveSelectedGraph(t.Context(), project, control)
		if err == nil || !strings.Contains(err.Error(), "missing [x/slot-b]") {
			t.Fatalf("control error = %v, want the base-closure refusal", err)
		}
	})

	t.Run("a second deploy module reached only through a selected adapter", func(t *testing.T) {
		seamA := slotSeam("x/system/seam-a", "x/slot-a")
		deployOne := deployModule("x/system/deploy-one")
		deployTwo := deployModule("x/system/deploy-two")
		smuggler := adapter("x/system/adapter-a", "x/slot-a", deployTwo.ID)
		root := Manifest{ID: "x/workflow/root", Kind: ModuleWorkflow, Contract: 1, Requires: requires(seamA.ID)}
		catalog := Catalog{Modules: []Manifest{seamA, deployOne, deployTwo, smuggler, root}}
		project := Project{
			Schema: 2, Modules: []string{root.ID}, Deployment: deployOne.ID,
			Providers: map[string]ProviderSelections{"x/slot-a": selections(smuggler.ID)},
		}
		_, err := resolveSelectedGraph(t.Context(), project, catalog)
		if err == nil || !strings.Contains(err.Error(), "multiple deployment modules selected") {
			t.Fatalf("error = %v, want the second deploy target refused", err)
		}

		// Control: one deploy module and nothing smuggled resolves cleanly,
		// so the refusal above is about the second target and not the shape.
		clean := Catalog{Modules: []Manifest{seamA, deployOne, adapter("x/system/adapter-a", "x/slot-a"), root}}
		if _, err := resolveSelectedGraph(t.Context(), project, clean); err != nil {
			t.Fatalf("the single-deployment control was refused: %v", err)
		}
	})

	t.Run("a slot declared only through the deployment module", func(t *testing.T) {
		seamB := slotSeam("x/system/seam-b", "x/slot-b")
		deployOne := deployModule("x/system/deploy-one", seamB.ID)
		root := Manifest{ID: "x/workflow/root", Kind: ModuleWorkflow, Contract: 1}
		catalog := Catalog{Modules: []Manifest{seamB, deployOne, root}}
		project := Project{Schema: 2, Modules: []string{root.ID}, Deployment: deployOne.ID,
			Providers: map[string]ProviderSelections{}}
		_, err := resolveSelectedGraph(t.Context(), project, catalog)
		if err == nil || !strings.Contains(err.Error(), "missing [x/slot-b]") {
			t.Fatalf("error = %v, want the slot the deployment module's requires reached named as missing", err)
		}
	})
}

// A claim is an EXCLUSIVE namespace reservation, so `claims.cli` with no
// `runtime.cli` record permanently reserves a `ggg` verb that no code
// implements and that no other module may then claim. The forward half
// (runtime ⊆ claims) was checked; the reverse was not, in validateManifest
// or in requireClaims — two lines below the job rule, which has always
// refused both directions.
func TestCLIClaimsAndDeclarationsRequireEachOther(t *testing.T) {
	base := Manifest{
		ID: "x/system/tool", Kind: ModuleSystem, Name: "tool", Revision: 1, Contract: 1,
		Title: "Tool", Description: "A module that contributes one ggg command.",
		Requires: []Requirement{}, Files: []ManifestFile{}, Migrations: []ManifestMigration{},
		Environment: []EnvironmentVariable{}, Docs: []DocumentationRef{}, Data: []DataDeclaration{},
		Dependencies:  Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		RemovalPolicy: RemovalFree,
		Claims:        NamespaceClaims{CLI: []string{"ui"}, Packages: []string{"internal/tool"}},
		Runtime: RuntimeContributions{
			CLI: []CLIContribution{{Name: "ui", Summary: "Open the tool.", Package: "internal/tool", Handler: "Run"}},
		},
	}
	if err := validateManifest(base, true); err != nil {
		t.Fatalf("the matched pair was refused: %v", err)
	}
	if err := requireClaims(base); err != nil {
		t.Fatalf("the matched pair was refused by requireClaims: %v", err)
	}

	claimOnly := base
	claimOnly.Runtime = RuntimeContributions{}
	if err := validateManifest(claimOnly, true); err == nil || !strings.Contains(err.Error(), `claims.cli "ui" has no runtime.cli declaration`) {
		t.Fatalf("validateManifest error = %v, want the reserved verb named", err)
	}
	if err := requireClaims(claimOnly); err == nil || !strings.Contains(err.Error(), "cli command ui") {
		t.Fatalf("requireClaims error = %v, want the reserved verb named", err)
	}

	declarationOnly := base
	declarationOnly.Claims = NamespaceClaims{Packages: []string{"internal/tool"}}
	if err := validateManifest(declarationOnly, true); err == nil || !strings.Contains(err.Error(), "requires a claims.cli entry") {
		t.Fatalf("validateManifest error = %v, want the unclaimed command named", err)
	}
	if err := requireClaims(declarationOnly); err == nil || !strings.Contains(err.Error(), "cli command ui") {
		t.Fatalf("requireClaims error = %v, want the unclaimed command named", err)
	}
}

func indexOf(items []string, want string) int {
	for i, item := range items {
		if item == want {
			return i
		}
	}
	return len(items)
}
func TestBootstrapBranchesByEnvironmentAndGatesAdapters(t *testing.T) {
	config := Manifest{ID: "ggg/system/config", Kind: ModuleSystem, Runtime: RuntimeContributions{System: &SystemContribution{
		Package: "internal/config", Constructor: "NewModule", Provides: []RuntimeProvide{{Field: "Config", Capability: "config", Type: "*config.Config"}},
	}}}
	local := Manifest{ID: "ggg/system/local", Kind: ModuleSystem, Runtime: RuntimeContributions{System: &SystemContribution{
		Package: "internal/local", Constructor: "New", Adapter: &AdapterContribution{Slot: "ggg/mail", Targets: []ServiceTarget{{ID: "filesystem", Title: "Filesystem", Mode: "development", Environments: []string{"development"}, Automation: "manual", DocsURL: "https://example.test"}}},
		Needs: []RuntimeNeed{{Field: "Config", Capability: "config", Type: "*config.Config"}}, Provides: []RuntimeProvide{{Field: "Sender", Capability: "mail.sender", Type: "any"}}, Start: true, Stop: true, Health: true,
	}}}
	remote := local
	remote.ID = "ggg/system/remote"
	remote.Runtime.System = &SystemContribution{Package: "internal/remote", Constructor: "New", Adapter: &AdapterContribution{Slot: "ggg/mail", Targets: []ServiceTarget{{ID: "managed", Title: "Managed", Mode: "managed", Environments: []string{"production"}, Automation: "configure", Provisioner: "p"}}}, Needs: []RuntimeNeed{{Field: "Config", Capability: "config", Type: "*config.Config"}}, Provides: []RuntimeProvide{{Field: "Sender", Capability: "mail.sender", Type: "any"}}, Health: true}
	lock := Lock{Schema: 2, RuntimeOrders: RuntimeOrders{Development: []string{config.ID, local.ID}, Test: []string{config.ID, local.ID}, Production: []string{config.ID, remote.ID}}, Providers: map[string]ProviderSelections{"ggg/mail": {Development: ProviderSelection{Adapter: local.ID, Target: "filesystem"}, Test: ProviderSelection{Adapter: local.ID, Target: "filesystem"}, Production: ProviderSelection{Adapter: remote.ID, Target: "managed"}}}, Modules: []LockedModule{{ID: config.ID}, {ID: local.ID}, {ID: remote.ID}}}
	out, err := emitBootstrapRegistry(context.Background(), "example.com/app", lock, []Manifest{config, local, remote})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"switch r.Config.Env", "bootDevelopment", "bootTest", "bootProduction", "providerActive", "var _ apphost.HealthChecker"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("bootstrap missing %q:\\n%s", want, out.Content)
		}
	}
	// One call per adapter: development and test both select local,
	// production selects remote, and no branch constructs the other. This
	// slot gets no accessor at all, because local declares start/stop and an
	// accessor has no way to run a lifecycle.
	if strings.Count(out.Content, "local.New(ctx") != 2 || strings.Count(out.Content, "remote.New(ctx") != 1 {
		t.Fatalf("adapter constructors should be selected per environment: len=%d", len(out.Content))
	}
	if strings.Contains(out.Content, "func MailSlotFor(") {
		t.Fatalf("a slot whose adapter has a lifecycle must not get an accessor:\n%s", out.Content)
	}
	// The exclusion says which adapter and which condition, because the three
	// reasons a slot can be excluded are three different next steps.
	if !strings.Contains(out.Content, "ggg/mail: ggg/system/local has a lifecycle an accessor cannot run") {
		t.Fatalf("bootstrap does not name why ggg/mail is excluded:\n%s", out.Content)
	}

	// Drop the lifecycle and the same slot becomes accessible: one extra
	// constructor call per environment arm, and the typed accessor appears.
	quiet := local
	quietSystem := *local.Runtime.System
	quietSystem.Start, quietSystem.Stop = false, false
	quiet.Runtime.System = &quietSystem
	accessible, err := emitBootstrapRegistry(context.Background(), "example.com/app", lock, []Manifest{config, quiet, remote})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type MailSlot struct", "func MailSlotFor(ctx context.Context, h apphost.Host, cfg "} {
		if !strings.Contains(accessible.Content, want) {
			t.Fatalf("bootstrap missing the per-slot accessor %q:\n%s", want, accessible.Content)
		}
	}
	if strings.Count(accessible.Content, "local.New(ctx") != 4 || strings.Count(accessible.Content, "remote.New(ctx") != 2 {
		t.Fatalf("accessor arms should construct exactly the selected adapter: len=%d", len(accessible.Content))
	}
	if !strings.Contains(out.Content, `Target: "managed"`) {
		t.Fatalf("production health registration did not persist selected target: %s", out.Content)
	}
}
func TestGeneratedExecutableContributionsCarryProviderGates(t *testing.T) {
	adapter := Manifest{ID: "ggg/system/mail-adapter", Kind: ModuleSystem, Runtime: RuntimeContributions{
		System:     &SystemContribution{Adapter: &AdapterContribution{Slot: "ggg/mail", Targets: []ServiceTarget{{ID: "local", Title: "Local", Mode: "development", Environments: []string{"development"}, Automation: "manual", DocsURL: "https://example.test"}}}},
		Routes:     []RouteContribution{{ID: "provider.route", Method: "GET", Pattern: "/provider", Scope: RoutePublic, Package: "internal/provider", Handler: "Show"}},
		Navigation: []NavigationContribution{{ID: "provider.nav", Area: NavAreaPublic, Href: "/provider", LabelKey: "provider"}},
		Slots:      []SlotContribution{{ID: "provider.slot", Slot: ShellSlotHead, Package: "internal/provider", Renderer: "Render"}},
		Assets:     []AssetContribution{{ID: "provider.asset", Path: "provider.js", Kind: AssetScript}},
		Jobs:       []JobContribution{{Kind: "provider.job", Package: "internal/jobs", Handler: "defineProviderJob"}},
	}}
	page := Manifest{ID: "ggg/page/provider", Kind: ModulePage}
	lock := Lock{Schema: 2, Order: []string{adapter.ID, page.ID}, RuntimeOrders: RuntimeOrders{Development: []string{adapter.ID, page.ID}, Test: []string{adapter.ID, page.ID}, Production: []string{adapter.ID}}}
	graph := []Manifest{adapter, page}
	routes, err := emitRoutesRegistry(context.Background(), "example.com/app", lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	chrome, err := emitChromeRegistry(context.Background(), "example.com/app", lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	slots, err := emitShellSlotsRegistry(context.Background(), "example.com/app", lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := emitStaticRegistry(context.Background(), "example.com/app", lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := emitJobsRegistry(context.Background(), "example.com/app", lock, graph)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{routes.Content, chrome.Content, slots.Content, assets.Content, jobs.Content} {
		if !strings.Contains(content, "providerActive") {
			t.Fatalf("executable contribution missing providerActive gate:\n%s", content)
		}
	}
	if !strings.Contains(slots.Content, "ShellSlotRenderers") ||
		!strings.Contains(slots.Content, "internal/provider") ||
		!strings.Contains(slots.Content, ".Render") {
		t.Fatalf("shell slot does not dispatch declared renderer:\n%s", slots.Content)
	}
	if !strings.Contains(jobs.Content, "defineProviderJob") {
		t.Fatalf("adapter-owned job missing from generated worker registry:\n%s", jobs.Content)
	}
}
