// Package static embeds the built frontend assets into the binary.
//
// The embed set itself is generated (embed_registry_gen.go) from module asset
// declarations, so every asset a selected module ships is compiled in and the
// compiler refuses a declared asset that is missing from the tree. app.css is
// generated (make generate) — never edit it by hand.
package static
