// Package static embeds the built frontend assets into the binary.
// app.css is generated (make generate) — never edit it by hand.
package static

import "embed"

//go:embed app.css app.js analytics.js favicon.svg site.webmanifest og.png vendor fonts
var FS embed.FS
