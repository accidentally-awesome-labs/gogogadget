package modkit

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// assetReference matches a `/static/...` URL where one actually starts: after a
// quote, an `=`, or whitespace. Without the boundary, `/ingest/static/array.js`
// — the analytics proxy path, which is a route and not an asset — matches on
// its tail and the check invents four failures for itself.
var assetReference = regexp.MustCompile(`(?:^|["'=\s(])(/static/[A-Za-z0-9._\-/]+)`)

// ValidateAssetReferences refuses a reference to an asset no installed module
// declares.
//
// Every ownership check in this engine asks whether a FILE has an OWNER. None
// asked whether a REFERENCE has a FILE, and the difference cost two published
// releases: moving `static/analytics.js` between manifests dropped its entry,
// sync removed the file as unowned, and the analytics adapter's head slot went
// on emitting `<script defer src="/static/analytics.js">` at a 404. Nothing
// could see it. The generated `//go:embed` list is built from declarations and
// its own comment says the compiler refuses a declared asset missing from the
// tree — true, and exactly inverted from this failure, where the declaration
// went away and the reference stayed.
//
// So this walks the other direction, and it catches the whole class rather than
// that incident: a typo in a path that was never right, a vendored bundle
// renamed by a version bump, an asset removed with its module while a seam
// still names it.
//
// Two things it deliberately does NOT do:
//
// It does not ask whether the file is on disk. The comparison is against the
// DECLARED set — every module's payload targets plus `runtime.assets` and
// `vendors` paths — because an asset is served only in the environments that
// select its owning module (`AssetEnabled`, from the same providerActive gate
// the routes use). A disk check would pass, and an environment check would
// refuse every development project for not serving Clerk's 55 bundles.
//
// It does not scan page and component markup. Scope is system-kind modules,
// the framework's own machinery that every project installs — the same
// boundary ValidateShellProviderNeutrality draws, for the same reason. A page
// module's fixture data naming an illustrative `/static/images/…` is content
// the project owns and edits; `ggg/page/dev-gallery`'s media-library scenario
// has six such paths today and refusing them would be enforcing a rule about
// somebody's demo copy. That finding belongs in a report, not in this gate.
func ValidateAssetReferences(modules []Manifest, files map[string][]byte) error {
	declared := declaredAssetPaths(modules)
	if len(declared) == 0 {
		return nil
	}

	// Declarations first: a declared asset or vendored payload with no file
	// behind it is unambiguous, whatever kind of module declared it.
	for _, module := range modules {
		owned := make(map[string]struct{}, len(module.Files))
		for _, file := range module.Files {
			owned[file.Target] = struct{}{}
		}
		for _, asset := range module.Runtime.Assets {
			target := assetTargetPath(asset.Path)
			if _, ok := owned[target]; !ok {
				return fmt.Errorf(
					"%s declares asset %s but owns no payload at %s; a declared asset needs the file that backs it",
					module.ID, asset.ID, target)
			}
		}
		for _, vendored := range module.Vendors {
			if _, ok := owned[vendored.Path]; !ok {
				return fmt.Errorf(
					"%s declares vendored asset %s but owns no payload at it; a vendored path needs the bytes it pins",
					module.ID, vendored.Path)
			}
		}
	}

	for _, module := range modules {
		if module.Kind != ModuleSystem {
			continue
		}
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			if file.Class == FileClassTest || strings.HasSuffix(file.Target, "_test.go") {
				continue
			}
			if !isReferencingPayload(file.Target) {
				continue
			}
			targets = append(targets, file.Target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			content, ok := files[target]
			if !ok {
				continue
			}
			for _, match := range assetReference.FindAllSubmatchIndex(content, -1) {
				reference := string(content[match[2]:match[3]])
				referenced := strings.TrimPrefix(reference, "/")
				// A path with no extension is a prefix or a directory —
				// `/static/ui/` in a design-system assertion, `/static/guides`
				// as a content root — not a file this can resolve.
				if path.Ext(referenced) == "" {
					continue
				}
				if _, ok := declared[referenced]; ok {
					continue
				}
				return fmt.Errorf(
					"%s references %s at %s:%d, which no installed module declares; either the asset lost its declaration (sync then removes the file and the reference keeps 404ing) or the path is wrong",
					module.ID, reference, target,
					1+strings.Count(string(content[:match[2]]), "\n"))
			}
		}
	}
	return nil
}

// declaredAssetPaths is every path an installed module claims to provide:
// payload targets, declared assets, and vendored bundles. Generated outputs
// count — `static/app.css` is declared with class `generated`, and a reference
// to it is satisfied by the declaration that makes the build produce it.
func declaredAssetPaths(modules []Manifest) map[string]string {
	declared := make(map[string]string)
	for _, module := range modules {
		for _, file := range module.Files {
			declared[file.Target] = module.ID
		}
		for _, asset := range module.Runtime.Assets {
			declared[assetTargetPath(asset.Path)] = module.ID
		}
		for _, vendored := range module.Vendors {
			declared[vendored.Path] = module.ID
		}
	}
	return declared
}

// assetTargetPath is the repository-relative payload a declared asset names.
// runtime.assets paths already carry the static/ prefix in every manifest in
// the catalog — the generated registry strips it rather than adding it — so
// prefixing here would look for static/static/ui/engines.js. Both shapes are
// accepted because the schema does not forbid either.
func assetTargetPath(declaredPath string) string {
	trimmed := strings.TrimPrefix(declaredPath, "/")
	if strings.HasPrefix(trimmed, "static/") {
		return trimmed
	}
	return "static/" + trimmed
}

// isReferencingPayload reports whether a payload is the kind of text that can
// name an asset URL: markup, the Go that emits markup or redirects, and the
// browser sources that fetch one another.
func isReferencingPayload(target string) bool {
	switch path.Ext(target) {
	case ".templ", ".go", ".js", ".css", ".html":
		return true
	default:
		return false
	}
}
