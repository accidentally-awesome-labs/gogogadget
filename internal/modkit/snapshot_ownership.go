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
// key-rotation record, and the five JSON Schema contracts the format is
// defined by.
//
// Enumerated, never pattern-matched. A `registry/schema/*.json` glob would
// make `registry/schema/anything.json` self-authorizing, and a
// `registry*.json` prefix rule would do the same at the root — which is the
// exact shape of hole this scan exists to close. Being on this list means
// "allowed when present", not "required": a third-party registry that ships
// no schema copies is complete without them, and the root-level signature and
// rotation files sit outside today's snapshot scope but are named here so a
// widened scope does not have to rediscover who owns them.
var registryFormatOwnedPaths = func() map[string]struct{} {
	owned := map[string]struct{}{
		"registry.json":                        {},
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
// Scope is exactly what gets signed: walkRegistrySnapshotScope drives both
// this scan and BuildRegistrySnapshot, so the gate cannot cover less than the
// signature does.
//
// Ownership resolves PER REGISTRY ROOT, not globally. registry/testdata and
// registry/external-testdata are complete registries nested inside the core
// payload root, each with its own registry.json, indexes and manifests, and
// their files are declared by THEIR manifests — a flat "must appear in some
// core files list" rule refuses all 79 of them plus every file under
// templates/external-registry. A nested root is recognised by its own
// registry.json and validated recursively against its own catalog, so nesting
// delegates ownership instead of escaping it.
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
	return validateRegistryTreeOwnership(fsys, "")
}

// validateRegistryTreeOwnership carries the prefix of the root being checked
// so a refusal inside a nested registry names the path as the enclosing
// snapshot lists it.
func validateRegistryTreeOwnership(fsys fs.FS, prefix string) error {
	root, err := loadRegistryRoot(fsys)
	if err != nil {
		return fmt.Errorf("registry ownership: %s%w", prefix, err)
	}
	declared, err := declaredRegistryPaths(fsys)
	if err != nil {
		return fmt.Errorf("registry ownership: %s%w", prefix, err)
	}
	nested := make([]string, 0)
	unowned := make([]string, 0)
	err = walkRegistrySnapshotScope(fsys, func(name string, entry fs.DirEntry) error {
		if entry.IsDir() {
			// A directory carrying its own registry.json is a registry root:
			// its contents answer to its own catalog, checked below, and the
			// enclosing catalog never declares them.
			if _, statErr := fs.Stat(fsys, name+"/registry.json"); statErr == nil {
				nested = append(nested, name)
				return fs.SkipDir
			}
			return nil
		}
		if _, ok := registryFormatOwnedPaths[name]; ok {
			return nil
		}
		// Tool output is owned by the tool. A `.templ` payload whose source
		// lives inside the registry tree gets a `_templ.go` sibling written
		// beside it by `make generate`, so demanding a manifest declaration
		// here would demand something the engine forbids outright:
		// reconcilePlannedState refuses a module that targets a generated
		// output ("generated outputs are tool-owned and cannot be authored"),
		// and the fixture registry's two committed `_templ.go` files reappear
		// on the next generate whatever this rule says. This repo has exactly
		// one home for that answer, IsGeneratedOutputPath, and this asks it
		// rather than keeping a second list. It is not a loophole a publisher
		// can hide bytes in either: DirectorySource.Resolve drops generated
		// outputs, so nothing in this class is ever installed into a project.
		if IsGeneratedOutputPath(name) {
			return nil
		}
		if _, ok := declared[name]; ok {
			return nil
		}
		unowned = append(unowned, name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("registry ownership: %s%w", prefix, err)
	}
	if len(unowned) != 0 {
		sort.Strings(unowned)
		// The root path leads, because the namespace alone is ambiguous here:
		// registry/testdata declares namespace "ggg" too, so "registry ggg"
		// would not say which of the two trees to look in.
		return fmt.Errorf(
			"registry root %q (namespace %q) publishes %d file(s) no module declares and the format does not own: %s; "+
				"declare each one in a manifest (files or migrations) or delete it — an undeclared payload inside a signed snapshot is bytes nothing installs",
			registryRootLabel(prefix), root.Namespace, len(unowned),
			prefix+strings.Join(unowned, ", "+prefix))
	}
	sort.Strings(nested)
	for _, name := range nested {
		sub, subErr := fs.Sub(fsys, name)
		if subErr != nil {
			return fmt.Errorf("registry ownership: open nested registry %s%s: %w", prefix, name, subErr)
		}
		if err := validateRegistryTreeOwnership(sub, prefix+name+"/"); err != nil {
			return err
		}
	}
	return nil
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
