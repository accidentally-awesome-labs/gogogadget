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
	// Two calls per adapter now: one in the environment's boot branch, one in
	// the per-slot accessor a CLI command uses instead of importing the
	// adapter. Development and test both select local, production selects
	// remote, and no branch or accessor arm constructs the other.
	if strings.Count(out.Content, "local.New(ctx") != 4 || strings.Count(out.Content, "remote.New(ctx") != 2 {
		t.Fatalf("adapter constructors should be selected per environment: len=%d", len(out.Content))
	}
	for _, want := range []string{"type MailSlot struct", "func MailSlotFor(ctx context.Context, h apphost.Host, cfg "} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("bootstrap missing the per-slot accessor %q:\n%s", want, out.Content)
		}
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
