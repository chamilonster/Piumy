package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFS embed.FS

// WebFS is the dashboard's static assets (index.html, style.css, app.js,
// agentpaste.js) —
// embed's own "web/" prefix stripped, so callers get an FS rooted at the
// files themselves. Compiled into the binary: no separate deploy step, no
// runtime file path to get wrong.
func WebFS() fs.FS {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // web/ is embedded at compile time -- can't fail at runtime
	}
	return sub
}
