package modkit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ValidateShellProviderNeutrality refuses a provider's name in seam-owned
// template bytes.
//
// The shell renders slots; adapters fill them. That boundary held everywhere
// except the render surface, where `ggg/system/server` owned a `<meta
// name="clerk-publishable-key">`, a `<script src="/static/vendor/clerk.browser.js">`
// and two `clerk-*-slot` classes — naming an asset and a mount contract that
// only ggg/system/identity-clerk installs. A project that deselected Clerk got
// a shell with dead mount points and no diagnostic, and `ggg/system/server`
// could not be installed without knowing two vendors' names. PostHog had the
// identical shape, which is what proved the mechanism was missing rather than
// the code sloppy.
//
// Scope is narrowed twice, deliberately.
//
// By owner: system-kind modules that are NOT provider adapters. Those are the
// framework's own machinery — the shell and the seams — and every project
// installs them, so a vendor name there is load-bearing on a module the
// project cannot remove. A page or component module is application content the
// project owns and edits; the marketing page that lists "Postgres · Clerk ·
// Polar · Resend" as the reference stack is prose about a default, not a
// mechanism, and refusing it would be wrong.
//
// By surface: template bytes — `.templ` payloads and the `.go` payloads that
// sit in a templates directory. That is where the leak was and where the slot
// mechanism now exists to replace it. The remaining named-vendor reads in
// internal/config and internal/web/routes.go are by env-key string, which is
// the sanctioned by-key read; they are not rendering decisions and this scan
// does not pretend to cover them.
//
// The token set is DERIVED from the installed graph, never listed here: an
// adapter that offers a managed service target names somebody's product, and
// that name is the token, together with the ids of its managed targets. An
// adapter with only development or self-hosted targets contributes no token —
// `mail-dev`, `observability-log`, `billing-local` and `rate-limit-memory`
// would otherwise ban the words "dev", "log", "local" and "memory" from every
// template in the tree. Matching requires a word start, so "probably" does not
// trip the Ably adapter and "Resend invitation" does not trip Resend.
func ValidateShellProviderNeutrality(modules []Manifest, files map[string][]byte) error {
	tokens := providerVendorTokens(modules)
	if len(tokens) == 0 {
		return nil
	}
	patterns := make(map[string]*regexp.Regexp, len(tokens))
	for token := range tokens {
		patterns[token] = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(token))
	}
	for _, module := range modules {
		if module.Kind != ModuleSystem || isProviderAdapter(module) {
			continue
		}
		targets := make([]string, 0, len(module.Files))
		for _, file := range module.Files {
			if isTemplateBytes(file.Target) {
				targets = append(targets, file.Target)
			}
		}
		sort.Strings(targets)
		for _, target := range targets {
			content, ok := files[target]
			if !ok {
				continue
			}
			for _, token := range sortedKeys(patterns) {
				match := patterns[token].FindIndex(content)
				if match == nil {
					continue
				}
				return fmt.Errorf(
					"%s owned by %s names provider %q (%s) at line %d; the shell must not know any provider — contribute the markup from %s as a runtime.slots renderer, which receives its own module's non-secret configuration and activates only in the environments that select the adapter",
					target, module.ID, token, tokens[token], 1+strings.Count(string(content[:match[0]]), "\n"), tokens[token])
			}
		}
	}
	return nil
}

// isProviderAdapter reports whether the module implements a provider slot.
func isProviderAdapter(module Manifest) bool {
	return module.Runtime.System != nil && module.Runtime.System.Adapter != nil
}

// isTemplateBytes reports whether a target path is part of the render surface:
// a templ source, or the Go beside it that the templates package compiles.
func isTemplateBytes(target string) bool {
	if strings.HasSuffix(target, ".templ") {
		return true
	}
	return strings.HasSuffix(target, ".go") && strings.Contains(target, "/templates/")
}

// providerVendorTokens maps a vendor name onto the adapter module that owns
// it. The adapter's name minus its slot's own name is one token — identity-clerk
// in slot ggg/identity yields "clerk" — and every managed target id is another,
// because storage-s3's managed target is called r2 and the vendor name is not
// derivable from the adapter's name alone.
func providerVendorTokens(modules []Manifest) map[string]string {
	tokens := make(map[string]string)
	for _, module := range modules {
		if !isProviderAdapter(module) {
			continue
		}
		adapter := module.Runtime.System.Adapter
		managed := make([]string, 0, len(adapter.Targets))
		for _, target := range adapter.Targets {
			if target.Mode == "managed" {
				managed = append(managed, target.ID)
			}
		}
		if len(managed) == 0 {
			continue
		}
		slot := adapter.Slot
		if index := strings.LastIndex(slot, "/"); index >= 0 {
			slot = slot[index+1:]
		}
		vendor := strings.TrimPrefix(module.Name, slot+"-")
		for _, token := range append(managed, vendor) {
			if token != "" {
				tokens[token] = module.ID
			}
		}
	}
	return tokens
}
