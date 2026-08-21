// Package content embeds the markdown collections (blog, docs, changelog)
// into the binary. Docs ship IN the app: they version with the code and are
// greppable by coding agents. The changelog does too, which is the point —
// a release note lands in the same commit as the release.
package content

import "embed"

//go:embed blog docs changelog
var FS embed.FS
