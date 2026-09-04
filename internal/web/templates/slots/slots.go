// Package slots is the shell's declared home for shell-slot renderers.
//
// A renderer is `func(context.Context, map[string]string) templ.Component`:
// the request context, and the CONTRIBUTING module's own declared non-secret
// configuration resolved by key. It is registered from a manifest's
// runtime.slots block, the generated registry in package templates imports it,
// and it renders only in the environments whose provider selection includes
// the owning module. Nothing here may import package templates — the arrow
// points one way — and nothing in the shell may name a provider, which is what
// ValidateShellProviderNeutrality holds.
//
// One package with per-module files, like internal/web/templates/ui: every
// file has one owner, and removing a module takes exactly its own file. A
// package per adapter would give each one a directory of its own, and removing
// the module would prune the directory out from under the templ output
// committed beside it.
package slots
