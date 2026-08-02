// Package content embeds the markdown collections (blog + docs) into the
// binary. Docs ship IN the app: they version with the code and are greppable
// by coding agents.
package content

import "embed"

//go:embed blog docs
var FS embed.FS
