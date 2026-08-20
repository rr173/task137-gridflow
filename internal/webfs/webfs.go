// Package webfs embeds the browser frontend so the single Go binary serves
// both the HTTP API and the page. The frontend source does not count toward
// backend Go file/line totals.
package webfs

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*

var content embed.FS

// Handler returns an http.Handler serving the embedded frontend at "/" and
// the static assets at their paths. Returns ok=false if no files are embedded.
func Handler() (http.Handler, bool) {
	sub, err := fs.Sub(content, "web")
	if err != nil {
		return nil, false
	}
	// content is present; serve the subtree.
	return http.FileServer(http.FS(sub)), true
}
