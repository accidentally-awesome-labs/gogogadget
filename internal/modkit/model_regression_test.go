package modkit

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testCommitA = "0123456789abcdef0123456789abcdef01234567"
	testCommitB = "1111111111111111111111111111111111111111"
	testDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testDigestB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testDigestC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func testLockedModule(id, digest string) LockedModule {
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		panic("invalid test module id")
	}
	_, kind, name := parts[0], parts[1], parts[2]
	source := "registry/modules/" + kind + "/" + name + "/" + name + ".go"
	target := "internal/modules/" + name + ".go"
	manifestFile := ManifestFile{
		Source: source,
		Target: target,
		Class:  FileClassGo,
		SHA256: digest,
	}
	return LockedModule{
		ID:                id,
		Revision:          1,
		Contract:          1,
		RegistryNamespace: "ggg", SnapshotSHA256: testCommitA, SourceCommit: testCommitA,
		Reason:     "explicit",
		RequiredBy: []string{},
		Manifest: Manifest{
			ID:            id,
			Kind:          ModuleKind(kind),
			Name:          name,
			Revision:      1,
			Contract:      1,
			Title:         name,
			Description:   "Test module " + name + ".",
			Requires:      []Requirement{},
			Dependencies:  Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
			Files:         []ManifestFile{manifestFile},
			Claims:        NamespaceClaims{},
			Runtime:       RuntimeContributions{},
			Migrations:    []ManifestMigration{},
			Environment:   []EnvironmentVariable{},
			Docs:          []DocumentationRef{},
			Tests:         TestMetadata{},
			Data:          []DataDeclaration{},
			RemovalPolicy: RemovalFree,
		},
		Files: []LockedFile{{
			Path:        target,
			Source:      source,
			BaseSHA256:  digest,
			LocalSHA256: digest,
			State:       FileClean,
		}},
		Migrations: []LockedMigration{},
	}
}

func testTwoModuleLock() Lock {
	base := testLockedModule("ggg/element/base", testDigestA)
	consumer := testLockedModule("ggg/component/consumer", testDigestB)
	consumer.Manifest.Requires = []Requirement{{ID: base.ID, Contract: ContractBounds{Min: 1, Max: 1}}}
	base.RequiredBy = []string{consumer.ID}
	return Lock{
		Schema:         2,
		RegistryCommit: testCommitA,
		Order:          []string{base.ID, consumer.ID},
		Modules:        []LockedModule{consumer, base},
	}
}

func marshalLockJSON(t *testing.T, lock Lock) []byte {
	t.Helper()
	if lock.Registries == nil {
		lock.Registries = []LockedRegistry{}
	}
	if lock.Snapshots == nil {
		lock.Snapshots = []LockedSnapshot{}
	}
	if lock.Dependencies == nil {
		lock.Dependencies = []LockedDependency{}
	}
	if lock.GoTools == nil {
		lock.GoTools = []string{}
	}
	if lock.RuntimeOrders.Development == nil {
		lock.RuntimeOrders.Development = append([]string{}, lock.Order...)
	}
	if lock.RuntimeOrders.Test == nil {
		lock.RuntimeOrders.Test = append([]string{}, lock.Order...)
	}
	if lock.RuntimeOrders.Production == nil {
		lock.RuntimeOrders.Production = append([]string{}, lock.Order...)
	}
	for i := range lock.Modules {
		m := &lock.Modules[i].Manifest
		if m.Dependencies.Go == nil {
			m.Dependencies.Go = []GoDependency{}
		}
		if m.Dependencies.Tools == nil {
			m.Dependencies.Tools = []ToolArtifact{}
		}
		if m.Dependencies.Containers == nil {
			m.Dependencies.Containers = []ContainerDependency{}
		}
	}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatalf("json.Marshal(lock): %v", err)
	}
	return data
}

func pendingTestLock() Lock {
	lock := testTwoModuleLock()
	lock.RegistryCommit = testCommitB

	module := &lock.Modules[0]
	module.Files[0].LocalSHA256 = testDigestA
	module.Files[0].State = FileConflicted
	target := module.Manifest
	target.Revision = 2
	target.Files = append([]ManifestFile(nil), target.Files...)
	target.Files[0].SHA256 = testDigestC
	module.Pending = &PendingUpdate{
		RunID:          "run-1",
		RegistryCommit: testCommitB,
		SourceCommit:   testCommitB,
		Manifest:       target,
		Conflicts: []PendingConflict{{
			Path:            module.Files[0].Path,
			CandidateSHA256: testDigestC,
			CandidatePath:   "tmp/ggg/conflicts/run-1/component-consumer/candidate.go",
			DiffPath:        "tmp/ggg/conflicts/run-1/component-consumer/candidate.diff",
		}},
	}
	module.Migrations = []LockedMigration{{
		ID: "consumer-forward", Number: 20,
		Path: "internal/db/migrations/0020_consumer.sql", SHA256: testDigestA,
	}}
	lock.Modules[1].Migrations = []LockedMigration{{
		ID: "base-forward", Number: 21,
		Path: "internal/db/migrations/0021_base.sql", SHA256: testDigestB,
	}}
	return lock
}

func TestMarshalProjectPreservesEmptyArrays(t *testing.T) {
	project := Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/profile/full"},
		Exclude: []string{},
	}

	data, err := MarshalProject(project)
	if err != nil {
		t.Fatalf("MarshalProject: %v", err)
	}
	if !strings.Contains(string(data), `"exclude": []`) {
		t.Fatalf("MarshalProject omitted empty exclude array:\n%s", data)
	}
	parsed, err := ParseProject(data)
	if err != nil {
		t.Fatalf("ParseProject(MarshalProject): %v", err)
	}
	if parsed.Exclude == nil {
		t.Fatal("parsed exclude is nil, want present empty array")
	}
}

func TestParseLockRejectsInconsistentReverseDependencies(t *testing.T) {
	t.Run("missing reverse edge", func(t *testing.T) {
		lock := testTwoModuleLock()
		lock.Modules[1].RequiredBy = []string{}
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "required_by") {
			t.Fatalf("ParseLock error = %v, want required_by mismatch", err)
		}
	})

	t.Run("unknown reverse dependent", func(t *testing.T) {
		lock := testTwoModuleLock()
		lock.Modules[1].RequiredBy = []string{"ggg/workflow/missing"}
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "required_by") {
			t.Fatalf("ParseLock error = %v, want required_by mismatch", err)
		}
	})
}

func TestParseLockRejectsUnsafeRuntimeReferences(t *testing.T) {
	t.Run("package injection", func(t *testing.T) {
		lock := testTwoModuleLock()
		lock.Modules[0].Manifest.Runtime.System = &SystemContribution{
			Package:     "example.com/acme/pkg;evil",
			Constructor: "New",
			Needs:       []RuntimeNeed{},
			Provides:    []RuntimeProvide{},
		}
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "package") {
			t.Fatalf("ParseLock error = %v, want unsafe package rejection", err)
		}
	})

	t.Run("type injection", func(t *testing.T) {
		lock := testTwoModuleLock()
		lock.Modules[0].Manifest.Runtime.System = &SystemContribution{
			Package:     "example.com/acme/pkg",
			Constructor: "New",
			Needs: []RuntimeNeed{{
				Field: "DB", Capability: "database", Type: "pkg.Type);evil",
			}},
			Provides: []RuntimeProvide{},
		}
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "type") {
			t.Fatalf("ParseLock error = %v, want unsafe type rejection", err)
		}
	})
}

func TestParseLockRejectsInvalidClosedContributions(t *testing.T) {
	tests := []struct {
		name string
		set  func(*RuntimeContributions)
		want string
	}{
		{
			name: "content mode",
			set: func(runtime *RuntimeContributions) {
				runtime.ContentTypes = []ContentTypeContribution{{
					ID: "guide", Mode: ContentMode("typo"), Paths: []string{"/guides"},
					Package: "example.com/acme/content", Handler: "Handle",
				}}
			},
			want: "mode",
		},
		{
			name: "navigation area",
			set: func(runtime *RuntimeContributions) {
				runtime.Navigation = []NavigationContribution{{
					ID: "guide", Area: NavArea("typo"), RouteID: "guide", LabelKey: "guide.label",
				}}
			},
			want: "area",
		},
		{
			name: "shell slot",
			set: func(runtime *RuntimeContributions) {
				runtime.Slots = []SlotContribution{{
					ID: "guide", Slot: ShellSlot("typo"), Package: "example.com/acme/ui", Renderer: "Guide",
				}}
			},
			want: "slot",
		},
		{
			name: "gallery family",
			set: func(runtime *RuntimeContributions) {
				runtime.UI = []UIContribution{{Signature: "templ X(o XOpts)", Name: "guide", Family: GalleryFamily("typo")}}
			},
			want: "family",
		},
		{
			name: "gallery component name",
			set: func(runtime *RuntimeContributions) {
				runtime.UI = []UIContribution{{Signature: "templ X(o XOpts)", Name: "Guide", Family: GalleryFeedback}}
			},
			want: "name",
		},
		{
			name: "asset kind",
			set: func(runtime *RuntimeContributions) {
				runtime.Assets = []AssetContribution{{ID: "guide", Path: "static/guide.js", Kind: AssetKind("typo")}}
			},
			want: "kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := testTwoModuleLock()
			tt.set(&lock.Modules[0].Manifest.Runtime)
			_, err := ParseLock(marshalLockJSON(t, lock))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseLock error = %v, want %s rejection", err, tt.want)
			}
		})
	}
}

func TestMarshalLockCanonicalizesWithoutMutation(t *testing.T) {
	lock := testTwoModuleLock()
	consumer := lock.Modules[0]
	zFile := consumer.Manifest.Files[0]
	zFile.Source = "registry/modules/component/consumer/z.go"
	zFile.Target = "internal/modules/z.go"
	aFile := zFile
	aFile.Source = "registry/modules/component/consumer/a.go"
	aFile.Target = "internal/modules/a.go"
	aFile.SHA256 = testDigestA
	consumer.Manifest.Files = []ManifestFile{zFile, aFile}
	consumer.Files = []LockedFile{
		{Path: zFile.Target, Source: zFile.Source, BaseSHA256: zFile.SHA256, LocalSHA256: zFile.SHA256, State: FileClean},
		{Path: aFile.Target, Source: aFile.Source, BaseSHA256: aFile.SHA256, LocalSHA256: aFile.SHA256, State: FileClean},
	}
	consumer.Manifest.Claims.Routes = []string{"z", "a"}
	consumer.Manifest.Runtime.Routes = []RouteContribution{
		{ID: "z", Method: "GET", Pattern: "/z", Scope: RoutePublic, Package: "example.com/acme/web", Handler: "HandleZ"},
		{ID: "a", Method: "GET", Pattern: "/a", Scope: RoutePublic, Package: "example.com/acme/web", Handler: "HandleA"},
	}
	consumer.Manifest.Environment = []EnvironmentVariable{
		{Key: "Z_KEY", Field: "Z", Description: "Z."},
		{Key: "A_KEY", Field: "A", Description: "A."},
	}
	consumer.Manifest.Docs = []DocumentationRef{
		{Path: "content/docs/z.md", Title: "Z"},
		{Path: "content/docs/a.md", Title: "A"},
	}
	consumer.Manifest.Tests.Capabilities = []string{"z", "a"}
	lock.Modules[0] = consumer
	lock.Modules = []LockedModule{lock.Modules[1], lock.Modules[0]}

	data, err := MarshalLock(lock)
	if err != nil {
		t.Fatalf("MarshalLock: %v", err)
	}
	canonical, err := ParseLock(data)
	if err != nil {
		t.Fatalf("ParseLock(MarshalLock): %v", err)
	}
	gotConsumer := canonical.Modules[0]
	if got, want := gotConsumer.ID, "ggg/component/consumer"; got != want {
		t.Fatalf("first canonical module = %q, want %q", got, want)
	}
	if got, want := gotConsumer.Files[0].Path, "internal/modules/a.go"; got != want {
		t.Fatalf("first canonical file = %q, want %q", got, want)
	}
	if got, want := gotConsumer.Manifest.Claims.Routes[0], "a"; got != want {
		t.Fatalf("first canonical route claim = %q, want %q", got, want)
	}
	if got, want := gotConsumer.Manifest.Runtime.Routes[0].ID, "a"; got != want {
		t.Fatalf("first canonical runtime route = %q, want %q", got, want)
	}
	if got, want := gotConsumer.Manifest.Environment[0].Key, "A_KEY"; got != want {
		t.Fatalf("first canonical environment key = %q, want %q", got, want)
	}
	if got, want := gotConsumer.Manifest.Docs[0].Path, "content/docs/a.md"; got != want {
		t.Fatalf("first canonical docs path = %q, want %q", got, want)
	}
	if got, want := gotConsumer.Manifest.Tests.Capabilities[0], "a"; got != want {
		t.Fatalf("first canonical test capability = %q, want %q", got, want)
	}
	if got, want := lock.Modules[0].ID, "ggg/element/base"; got != want {
		t.Fatalf("MarshalLock mutated caller module order: first = %q", got)
	}
	if got, want := lock.Modules[1].Manifest.Files[0].Target, "internal/modules/z.go"; got != want {
		t.Fatalf("MarshalLock mutated caller nested file order: first = %q", got)
	}
}

func TestParseLockPendingAndMigrationInvariants(t *testing.T) {
	t.Run("valid pending update", func(t *testing.T) {
		lock := pendingTestLock()
		if _, err := ParseLock(marshalLockJSON(t, lock)); err != nil {
			t.Fatalf("ParseLock(valid pending): %v", err)
		}
	})

	t.Run("conflicted file requires pending", func(t *testing.T) {
		lock := pendingTestLock()
		lock.Modules[0].Pending = nil
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "pending") {
			t.Fatalf("ParseLock error = %v, want pending requirement", err)
		}
	})

	t.Run("candidate digest matches pending manifest", func(t *testing.T) {
		lock := pendingTestLock()
		lock.Modules[0].Pending.Conflicts[0].CandidateSHA256 = testDigestB
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "candidate_sha256") {
			t.Fatalf("ParseLock error = %v, want candidate digest mismatch", err)
		}
	})

	t.Run("migration numbers are globally unique", func(t *testing.T) {
		lock := pendingTestLock()
		lock.Modules[1].Migrations[0].Number = lock.Modules[0].Migrations[0].Number
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "duplicate number") {
			t.Fatalf("ParseLock error = %v, want duplicate migration number", err)
		}
	})

	t.Run("test-only pending manifest is forbidden", func(t *testing.T) {
		lock := pendingTestLock()
		lock.Modules[0].Pending.Manifest.TestOnly = true
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "test_only") {
			t.Fatalf("ParseLock error = %v, want test_only rejection", err)
		}
	})
}

func TestLockRewrittenBaseDigestInvariant(t *testing.T) {
	rewrittenBase := strings.Replace(
		canonicalLockJSON,
		`"base_sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`,
		`"base_sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"`,
		1,
	)
	if _, err := ParseLock([]byte(rewrittenBase)); err != nil {
		t.Fatalf("ParseLock(rewritten base): %v", err)
	}

	notRewritten := strings.Replace(rewrittenBase, `"rewrite_module": true`, `"rewrite_module": false`, 1)
	_, err := ParseLock([]byte(notRewritten))
	if err == nil || !strings.Contains(err.Error(), "does not match manifest sha256") {
		t.Fatalf("ParseLock(non-rewritten mismatch) error = %v, want base/source mismatch rejection", err)
	}
}

// A trailing slash is Go's subtree pattern, and real surfaces need it:
// "/static/" serves an asset tree and "/debug/pprof/" serves the profiler index.
// Rejecting it would make those routes undeclarable, which pushes them back into
// hand-written registration and out of the policy matcher.
func TestValidateRoutePathAcceptsSubtreePatterns(t *testing.T) {
	valid := []string{"/", "/static/", "/debug/pprof/", "/app/", "/api/v1/projects", "/thing/{id}"}
	for _, path := range valid {
		if err := validateRoutePath(path); err != nil {
			t.Errorf("validateRoutePath(%q) = %v, want nil", path, err)
		}
	}
	invalid := []string{"", "static/", "/a//b", "/../etc", "/a/./b", "//host/path"}
	for _, path := range invalid {
		if err := validateRoutePath(path); err == nil {
			t.Errorf("validateRoutePath(%q) = nil, want an error", path)
		}
	}
}

// A UI component name is the value rendered as data-ui on the component root,
// so the manifest must accept exactly the kebab-case shape the markup uses and
// reject shapes that could never appear as one attribute value.
func TestValidateUIAcceptsKebabCaseComponentNames(t *testing.T) {
	for _, name := range []string{"badge", "alert-dialog", "table-card"} {
		if err := validateUI([]UIContribution{{Signature: "templ X(o XOpts)", Name: name, Family: GalleryFeedback}}, true); err != nil {
			t.Fatalf("%q is a real rendered data-ui value: %v", name, err)
		}
	}
	for _, bad := range []string{"", "Badge", "alert_dialog", "-badge", "badge-", "alert--dialog", "badge 2"} {
		if err := validateUI([]UIContribution{{Signature: "templ X(o XOpts)", Name: bad, Family: GalleryFeedback}}, true); err == nil {
			t.Fatalf("%q cannot be a data-ui value but was accepted", bad)
		}
	}
}

// An engine asset with no integrity is a lazily injected script that can be
// swapped without anything noticing, so the manifest must not express one.
func TestValidateAssetsRequiresIntegrityForEngines(t *testing.T) {
	engine := func(integrity string) []AssetContribution {
		return []AssetContribution{{
			ID: "chartjs", Path: "static/vendor/chart.js", Kind: AssetScript,
			Engine: "chartjs", Integrity: integrity,
		}}
	}
	if err := validateAssets(engine("sha256-AAAA"), true); err != nil {
		t.Fatalf("a pinned engine asset is valid: %v", err)
	}
	if err := validateAssets(engine(""), true); err == nil {
		t.Fatal("an engine asset with no integrity must be rejected")
	}

	// An integrity value with no engine is meaningless: the shell loads its own
	// scripts from tags the templates own, where the integrity would be ignored.
	orphan := []AssetContribution{{
		ID: "ui-overlays", Path: "static/ui/overlays.js", Kind: AssetScript,
		Integrity: "sha256-AAAA",
	}}
	if err := validateAssets(orphan, true); err == nil {
		t.Fatal("integrity without an engine must be rejected")
	}

	// The engine name reaches a data attribute selector, so it follows the same
	// kebab-case rule component names do.
	bad := engine("sha256-AAAA")
	bad[0].Engine = "ChartJS"
	if err := validateAssets(bad, true); err == nil {
		t.Fatal("an engine name that cannot appear in an attribute selector must be rejected")
	}
}

// The tests in this file pin the two-sided rule the lock/authoring
// validation split implements (see the comment on validateManifest):
//
//   - a lock row's embedded manifest is a historical RECORD — legal under
//     the era that wrote it — so lock parsing validates record integrity
//     only, and every era's lock must parse under today's binary;
//   - tamper detection for those same rows must stay fully armed: a
//     corrupt record, a file row that stops matching its embedded
//     manifest, or a row that stops covering its declared targets still
//     refuses loudly;
//   - the authoring rules themselves still exist and still gate every
//     manifest entering the system (catalog parse, `ggg create`).

// eraJobsLock builds a lock whose single row embeds a v0.16.0-era
// `ggg/system/billing` shape: five jobs declared in runtime.jobs while
// claims.jobs is empty — legal when written, refused by every authoring
// rule since v0.16.0 (finding P0-1's exact refusal).
func eraJobsLock() Lock {
	module := testLockedModule("ggg/system/billing", testDigestA)
	module.Manifest.Runtime.Jobs = []JobContribution{
		{Kind: "email.dunning_final", Package: "internal/billing", Handler: "HandleDunningFinal"},
	}
	return Lock{
		Schema:         2,
		EngineContract: 0, // written before the guard existed
		RegistryCommit: testCommitA,
		Order:          []string{module.ID},
		Modules:        []LockedModule{module},
	}
}

// eraIdempotentLock builds a lock whose single row embeds a v0.16.0-era
// `ggg/system/billing-local` shape: `idempotent: true` on an `app`-scope
// POST — legal when written, refused by the authoring rule first shipped
// v0.17.0 (finding P0-2's exact refusal).
func eraIdempotentLock() Lock {
	module := testLockedModule("ggg/system/billing-local", testDigestA)
	module.Manifest.Runtime.Routes = []RouteContribution{
		{ID: "app.billing.confirm", Method: "POST", Pattern: "/app/billing/confirm",
			Scope: RouteApp, Policy: RoutePolicy{Idempotent: true},
			Package: "internal/billing/local", Handler: "confirm"},
	}
	return Lock{
		Schema:         2,
		EngineContract: 0,
		RegistryCommit: testCommitA,
		Order:          []string{module.ID},
		Modules:        []LockedModule{module},
	}
}

func TestParseLockAcceptsEraManifests(t *testing.T) {
	// Each case is a manifest shape that WAS legal, stopped being legal for
	// new authoring at a named release, and must never stop the lock that
	// records it from parsing — the update that would replace the row is
	// the remedy, and it cannot even plan while the lock refuses to parse.
	t.Run("pre-v0.16 jobs without claims parse (P0-1)", func(t *testing.T) {
		if _, err := ParseLock(marshalLockJSON(t, eraJobsLock())); err != nil {
			t.Fatalf("an era lock must parse under record validation: %v", err)
		}
	})
	t.Run("pre-v0.17 app-scope idempotent route parses (P0-2)", func(t *testing.T) {
		if _, err := ParseLock(marshalLockJSON(t, eraIdempotentLock())); err != nil {
			t.Fatalf("an era lock must parse under record validation: %v", err)
		}
	})
	t.Run("an era-shaped pending manifest parses", func(t *testing.T) {
		lock := eraJobsLock()
		module := &lock.Modules[0]
		module.Files[0].LocalSHA256 = testDigestB
		module.Files[0].State = FileConflicted
		pending := module.Manifest
		pending.Revision = 2
		pending.Files = append([]ManifestFile(nil), pending.Files...)
		pending.Files[0].SHA256 = testDigestB
		module.Pending = &PendingUpdate{
			RunID: "run1", RegistryCommit: testCommitB, SourceCommit: testCommitB,
			Manifest: pending,
			Conflicts: []PendingConflict{{
				Path: module.Files[0].Path, CandidatePath: "tmp/ggg/conflicts/run1/x.go",
				DiffPath: "tmp/ggg/conflicts/run1/x.go.diff", CandidateSHA256: testDigestB,
			}},
		}
		if _, err := ParseLock(marshalLockJSON(t, lock)); err != nil {
			t.Fatalf("an era-shaped pending manifest must parse under record validation: %v", err)
		}
	})
	t.Run("both era shapes in one lock parse", func(t *testing.T) {
		jobs := eraJobsLock()
		idem := eraIdempotentLock()
		lock := Lock{
			Schema: 2, RegistryCommit: testCommitA,
			Order:   []string{jobs.Modules[0].ID, idem.Modules[0].ID},
			Modules: []LockedModule{jobs.Modules[0], idem.Modules[0]},
		}
		if _, err := ParseLock(marshalLockJSON(t, lock)); err != nil {
			t.Fatalf("a mixed-era lock must parse under record validation: %v", err)
		}
	})
}

func TestParseLockStillRefusesTamperedEraRecords(t *testing.T) {
	// Every case starts from an era lock proven to parse above and corrupts
	// exactly one record-integrity property. The relaxed authoring tier
	// must not have relaxed any of these.
	base := func() Lock {
		jobs := eraJobsLock()
		idem := eraIdempotentLock()
		idem.Modules[0].Manifest.Requires = []Requirement{{ID: jobs.Modules[0].ID, Contract: ContractBounds{Min: 1, Max: 1}}}
		jobs.Modules[0].RequiredBy = []string{idem.Modules[0].ID}
		return Lock{
			Schema: 2, RegistryCommit: testCommitA,
			Order:   []string{jobs.Modules[0].ID, idem.Modules[0].ID},
			Modules: []LockedModule{jobs.Modules[0], idem.Modules[0]},
		}
	}

	t.Run("file row stops matching its embedded manifest digest", func(t *testing.T) {
		lock := base()
		lock.Modules[0].Files[0].BaseSHA256 = testDigestC
		lock.Modules[0].Files[0].LocalSHA256 = testDigestC
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "does not match manifest sha256") {
			t.Fatalf("ParseLock error = %v, want the base/manifest digest coupling to refuse", err)
		}
	})
	t.Run("embedded manifest digest is structurally corrupt", func(t *testing.T) {
		lock := base()
		lock.Modules[0].Manifest.Files[0].SHA256 = "not-a-digest"
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "sha256 is invalid") {
			t.Fatalf("ParseLock error = %v, want invalid manifest digest to refuse", err)
		}
	})
	t.Run("row stops covering a declared manifest target", func(t *testing.T) {
		lock := base()
		lock.Modules[0].Manifest.Files = append(lock.Modules[0].Manifest.Files, ManifestFile{
			Source: "registry/modules/system/billing/extra.go",
			Target: "internal/modules/billing/extra.go",
			Class:  FileClassGo, SHA256: testDigestB,
		})
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "must cover every manifest file target") {
			t.Fatalf("ParseLock error = %v, want uncovered manifest target to refuse", err)
		}
	})
	t.Run("identity coupling between row and manifest breaks", func(t *testing.T) {
		lock := base()
		lock.Modules[0].Revision = 7
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "identity/revision/contract") {
			t.Fatalf("ParseLock error = %v, want row/manifest identity coupling to refuse", err)
		}
	})
	t.Run("order stops matching the dependency edges", func(t *testing.T) {
		lock := base()
		lock.Order = []string{lock.Modules[1].ID, lock.Modules[0].ID} // consumer first
		_, err := ParseLock(marshalLockJSON(t, lock))
		if err == nil || !strings.Contains(err.Error(), "places dependency") {
			t.Fatalf("ParseLock error = %v, want order/dependency coupling to refuse", err)
		}
	})
}

func TestAuthoringPolicyStillGatesManifestsEnteringTheSystem(t *testing.T) {
	t.Run("claims.jobs pairing refuses for authoring", func(t *testing.T) {
		manifest := eraJobsLock().Modules[0].Manifest
		err := ValidateManifest(manifest)
		if err == nil || !strings.Contains(err.Error(), `runtime jobs "email.dunning_final" requires a claims.jobs entry`) {
			t.Fatalf("ValidateManifest error = %v, want the claims.jobs pairing to refuse new authoring", err)
		}
	})
	t.Run("idempotent scope refuses for authoring", func(t *testing.T) {
		manifest := eraIdempotentLock().Modules[0].Manifest
		err := ValidateManifest(manifest)
		if err == nil || !strings.Contains(err.Error(), "idempotent is enforced only on the api-read and api-write scopes") {
			t.Fatalf("ValidateManifest error = %v, want the idempotent scope rule to refuse new authoring", err)
		}
	})
	t.Run("idempotent safe method refuses for authoring", func(t *testing.T) {
		manifest := eraIdempotentLock().Modules[0].Manifest
		manifest.Runtime.Routes[0].Method = "GET"
		manifest.Runtime.Routes[0].Scope = RouteAPIRead
		err := ValidateManifest(manifest)
		if err == nil || !strings.Contains(err.Error(), "idempotent is meaningless on the safe method") {
			t.Fatalf("ValidateManifest error = %v, want the safe-method rule to refuse new authoring", err)
		}
	})
}
