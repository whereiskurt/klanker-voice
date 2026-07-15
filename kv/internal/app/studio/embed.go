package studio

import "embed"

// WebFS embeds the studio console's self-contained web assets (Plan 15-03's
// index.html/app.js/styles.css) directly into the kv binary — no CDN, no
// external fetch, matching the design spec's local-only/self-contained
// console requirement. Handler() serves it via fs.Sub(WebFS, "web") so the
// public URL space is rooted at "/", not "/web/".
//
//go:embed web
var WebFS embed.FS
