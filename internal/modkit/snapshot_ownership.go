package modkit

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// registryFormatOwnedPaths are the registry-relative files the FORMAT owns
// rather than any module: the root document, the six kind indexes, the
// snapshot with its signature and the two detached rotation signatures, the
// key-rotation record, the five JSON Schema contracts the format is defined
// by, and the `.gitignore` InitRegistryTree scaffolds to keep signing keys
// out of version control.
//
// Enumerated, never pattern-matched. A `registry/schema/*.json` glob would
// make `registry/schema/anything.json` self-authorizing, and a
// `registry*.json` prefix rule would do the same at the root — which is the
// exact shape of hole this scan exists to close. Being on this list means
// "allowed when present", not "required": a third-party registry that ships
// no schema copies is complete without them.
//
// These are root-relative, and every lookup strips the owning root's prefix
// first, so a nested root's own `registry.snapshot.json` and `.gitignore` are
// format-owned files OF THAT ROOT while the enclosing signature covers them.
// The outer root's copies mostly sit outside the snapshot scope; they are
// named anyway so a widened scope does not have to rediscover who owns them.
var registryFormatOwnedPaths = func() map[string]struct{} {
	owned := map[string]struct{}{
		"registry.json":                        {},
		".gitignore":                           {},
		RegistrySnapshotPath:                   {},
		RegistrySignaturePath:                  {},
		RegistryKeyRotationPath:                {},
		registryRotationOldSigPath:             {},
		registryRotationNewSigPath:             {},
		"registry/schema/registry.schema.json": {},
		"registry/schema/module.schema.json":   {},
		"registry/schema/project.schema.json":  {},
		"registry/schema/lock.schema.json":     {},
		"registry/schema/snapshot.schema.json": {},
	}
	for _, include := range catalogIncludes {
		owned[include.path] = struct{}{}
	}
	return owned
}()

// ValidateRegistryTreeOwnership refuses a file inside the signed registry
// scope that neither the format owns nor any catalogued module declares.
//
// This is the inverse of ValidateAssetReferences, and both directions were
// half-open. That one asks whether a REFERENCE has a FILE. Every other
// ownership check in this engine asks whether a project file has a module
// owner. None asked whether a file in the PUBLISHED CATALOG has a declaring
// owner, and the asymmetry cost a signed artifact: six index files written by
// a `registry build` that then refused, picked up by the next build, listed
// in registry.snapshot.json, and signed. `make check`, `registry validate`,
// `make e2e` and `sync --offline` all passed with them inside the signature.
// A payload no manifest owns, inside a correctly-signed catalog, is bytes
// nobody declared and nothing installs — and the signature makes it look
// deliberate.
//
// Scope is exactly what gets signed, and sharing walkRegistrySnapshotScope
// with BuildRegistrySnapshot is necessary but NOT sufficient for that: the
// first version shared the walker and still covered less, because it pruned
// each nested root and re-entered with a narrower scope. The invariant is
// coverage, not provenance, and TestOwnershipCoverageIsASupersetOfTheSignedScope
// asserts it over a nested fixture.
//
// Ownership resolves PER REGISTRY ROOT, not globally. registry/testdata and
// registry/external-testdata are complete registries nested inside the core
// payload root, each with its own registry.json, indexes and manifests, and
// their files are declared by THEIR manifests — a flat "must appear in some
// core files list" rule refuses all 79 of them plus every file under
// templates/external-registry. One walk covers everything the signature
// covers and answers each file against the innermost root containing it, so
// nesting delegates ownership instead of escaping it or hiding it.
//
// Two things it deliberately does NOT do:
//
// It does not delete. `ggg sync` sweeps unowned files out of a project tree
// silently, which is how a stray under a module-owned directory disappears
// today. A signed catalog that gains and loses undeclared bytes without a
// word is the problem, not the remedy: this refuses, names the path, and
// leaves the operator to decide whether the file wants a declaration or a
// deletion.
//
// It does not read the index items through the loaded Catalog. The catalog
// keeps manifests, not the document paths that carry them, and reconstructing
// `registry/modules/<kind>/<name>/module.json` from a module id would make
// the gate agree with its own guess. The indexes are the registry's own
// statement of which documents belong to it, so they are read as such.
func ValidateRegistryTreeOwnership(fsys fs.FS) error {
	report, err := registryTreeOwnership(fsys)
	if err != nil {
		return err
	}
	return report.refusal()
}

// ownershipRoot is one registry root discovered during the walk, with the
// paths its own catalog declares. prefix is "" for the outer root and
// "<dir>/" for a nested one, so a path is resolved against a root by
// stripping the prefix.
type ownershipRoot struct {
	prefix    string
	namespace string
	declared  map[string]struct{}
}

// registryOwnershipReport is what one pass observed. examined exists so a
// test can assert the invariant that actually matters — gate coverage is a
// superset of BuildRegistrySnapshot coverage — which sharing the walker is
// necessary but NOT sufficient for. The first version of this gate shared the
// walker and still missed two files in the committed signature, because it
// added a prune the builder does not have.
type registryOwnershipReport struct {
	examined []string
	unowned  map[string][]string
	roots    []ownershipRoot
}

func (r registryOwnershipReport) refusal() error {
	if len(r.unowned) == 0 {
		return nil
	}
	labels := make([]string, 0, len(r.unowned))
	for prefix := range r.unowned {
		labels = append(labels, prefix)
	}
	sort.Strings(labels)
	clauses := make([]string, 0, len(labels))
	for _, prefix := range labels {
		paths := r.unowned[prefix]
		sort.Strings(paths)
		namespace := ""
		for _, root := range r.roots {
			if root.prefix == prefix {
				namespace = root.namespace
			}
		}
		// The root path leads, because the namespace alone is ambiguous here:
		// registry/testdata declares namespace "ggg" too, so "registry ggg"
		// would not say which of the two trees to look in.
		clauses = append(clauses, fmt.Sprintf(
			"registry root %q (namespace %q) publishes %d file(s) no module declares and the format does not own: %s",
			registryRootLabel(prefix), namespace, len(paths), strings.Join(paths, ", ")))
	}
	return fmt.Errorf("%s; declare each one in a manifest (files or migrations) or delete it — "+
		"an undeclared payload inside a signed snapshot is bytes nothing installs",
		strings.Join(clauses, "; "))
}

// registryTreeOwnership walks the signed scope ONCE and answers each file
// against the innermost registry root that contains it.
//
// It deliberately does not prune a nested root and recurse. That was the
// first shape and it was wrong: fs.SkipDir removed the whole nested directory
// from this walk, and the recursion re-entered with a scope of only
// `registry.json` + `registry/**` RELATIVE TO the nested root — so a nested
// root's own top level was covered by the enclosing signature and examined by
// nobody. `registry/external-testdata/registry.snapshot.json` and its
// signature shipped inside the core signature through exactly that gap.
//
// Widening the shared walk instead would have widened what every inner
// signature covers, which is a different slice. Resolving prefix-relative in
// one pass fixes the coverage hole without touching the scope either the
// builder or the gate uses.
func registryTreeOwnership(fsys fs.FS) (registryOwnershipReport, error) {
	outer, err := loadRegistryRoot(fsys)
	if err != nil {
		return registryOwnershipReport{}, fmt.Errorf("registry ownership: %w", err)
	}
	declared, err := declaredRegistryPaths(fsys)
	if err != nil {
		return registryOwnershipReport{}, fmt.Errorf("registry ownership: %w", err)
	}
	report := registryOwnershipReport{
		examined: make([]string, 0),
		unowned:  map[string][]string{},
		roots:    []ownershipRoot{{prefix: "", namespace: outer.Namespace, declared: declared}},
	}
	err = walkRegistrySnapshotScope(fsys, func(name string, entry fs.DirEntry) error {
		if entry.IsDir() {
			// A directory carrying its own registry.json is a registry root:
			// everything under it answers to ITS catalog instead of the
			// enclosing one. A malformed or incomplete nested registry is a
			// refusal, not an exemption — conjuring a root takes a valid
			// registry.json AND all six parseable indexes, and its own files
			// then have to be declared by its own manifests.
			if _, statErr := fs.Stat(fsys, name+"/registry.json"); statErr != nil {
				return nil
			}
			sub, subErr := fs.Sub(fsys, name)
			if subErr != nil {
				return fmt.Errorf("registry ownership: open nested registry %s: %w", name, subErr)
			}
			nested, rootErr := loadRegistryRoot(sub)
			if rootErr != nil {
				return fmt.Errorf("registry ownership: nested registry %s: %w", name, rootErr)
			}
			nestedDeclared, declErr := declaredRegistryPaths(sub)
			if declErr != nil {
				return fmt.Errorf("registry ownership: nested registry %s: %w", name, declErr)
			}
			report.roots = append(report.roots, ownershipRoot{
				prefix: name + "/", namespace: nested.Namespace, declared: nestedDeclared,
			})
			return nil
		}
		report.examined = append(report.examined, name)
		// Innermost root wins, so a registry nested inside a registry nested
		// inside a registry resolves against the one that actually publishes
		// the file. Roots are discovered in pre-order, so every root
		// containing this path is already known.
		owner := ownershipRoot{}
		for _, root := range report.roots {
			if root.prefix != "" && !strings.HasPrefix(name, root.prefix) {
				continue
			}
			if len(root.prefix) >= len(owner.prefix) {
				owner = root
			}
		}
		relative := strings.TrimPrefix(name, owner.prefix)
		if _, ok := registryFormatOwnedPaths[relative]; ok {
			return nil
		}
		if _, ok := owner.declared[relative]; ok {
			return nil
		}
		if registryToolOwnedPath(relative, owner.declared) {
			return nil
		}
		report.unowned[owner.prefix] = append(report.unowned[owner.prefix], name)
		return nil
	})
	if err != nil {
		return registryOwnershipReport{}, err
	}
	return report, nil
}

// registryToolOwnedPath reports whether one path is templ output whose
// authored source the SAME registry declares.
//
// The rule is anchored to a declared sibling, and that is the whole point.
// The first version asked IsGeneratedOutputPath, which matches an INFIX
// `_registry_gen.` with any extension at any depth plus any `_templ.go`
// suffix — so a file could adopt an exempt name and ride into a signed
// snapshot invisibly. `registry/modules/system/modkit/planted_registry_gen.txt`
// was measured doing exactly that: build exit 0, listed in the snapshot, both
// self-host tests green. Only the filename separated it from a correct
// refusal.
//
// So the class exists for one real reason and is no wider than that reason.
// `make generate` runs templ over the whole tree and writes `X_templ.go`
// beside every `X.templ`, including the two in the fixture registry — and a
// module cannot declare the output instead, because reconcilePlannedState
// refuses "generated outputs are tool-owned and cannot be authored". Tying the
// exemption to the declared `.templ` keeps regeneration working (editing the
// source rewrites the sibling and it stays owned) while an orphan
// `X_templ.go` with no declared `X.templ` refuses.
//
// IsRegistryOwnedOutputPath is deliberately not consulted: every path it names
// is a project path and GenerateAll renders into a project tree, never into a
// catalog, so it has no legitimate instance inside a registry root.
func registryToolOwnedPath(relative string, declared map[string]struct{}) bool {
	stem, isTemplOutput := strings.CutSuffix(relative, "_templ.go")
	if !isTemplOutput {
		return false
	}
	_, declaredSource := declared[stem+".templ"]
	return declaredSource
}

// registryRootLabel renders a nested-root prefix as a path. The outermost
// root has no prefix and is named "." rather than "".
func registryRootLabel(prefix string) string {
	if prefix == "" {
		return "."
	}
	return strings.TrimSuffix(prefix, "/")
}

// declaredRegistryPaths is every registry-relative path the catalogued
// modules of one registry claim: the document that carries each manifest,
// plus each manifest's payload and migration sources. A profile document has
// no payloads; its own path is what the index declares.
//
// Payload sources are read from every catalogued module, not only the
// selected graph. A registry publishes its whole catalog and signs its whole
// tree, so "installed" is the wrong question here: a project that selects
// twelve modules still receives a snapshot listing all 240, and scoping the
// declaration side to a selection would refuse every unselected module's
// payloads in every project.
func declaredRegistryPaths(fsys fs.FS) (map[string]struct{}, error) {
	declared := make(map[string]struct{})
	for _, include := range catalogIncludes {
		var index CatalogIndex
		if err := readCatalogJSON(fsys, include.path, &index); err != nil {
			return nil, err
		}
		for _, item := range index.Items {
			if err := validateCatalogItemPath(include.kind, item); err != nil {
				return nil, fmt.Errorf("%s items: %w", include.path, err)
			}
			declared[item] = struct{}{}
			if include.kind == CatalogProfile {
				continue
			}
			var document ModuleDocument
			if err := readCatalogJSON(fsys, item, &document); err != nil {
				return nil, err
			}
			for _, file := range document.Module.Files {
				declared[file.Source] = struct{}{}
			}
			for _, migration := range document.Module.Migrations {
				declared[migration.Source] = struct{}{}
			}
		}
	}
	return declared, nil
}
