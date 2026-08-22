// Package content embeds the markdown collections (blog, docs, changelog)
// into the binary. Docs ship IN the app: they version with the code and are
// greppable by coding agents.
//
// Blog and changelog are NOT a runtime read path any more. They are the SEED
// CORPUS: cmd/seed imports them into content_entries (idempotently, by
// kind+slug) and every request after that reads the database, which is what
// makes them editable at /admin/content without a deploy. They stay embedded
// so a fresh clone seeds a populated site and the docs link-checker keeps
// walking them.
package content

import "embed"

//go:embed blog docs changelog
var FS embed.FS
