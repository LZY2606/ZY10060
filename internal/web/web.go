// Package web serves the single-page browser interface. It is a view: it
// never performs a metrological judgment itself.
package web

import (
	"embed"
	"net/http"
	"strings"
)

//go:embed static/index.html static/app.js static/style.css
var staticFS embed.FS

var allowed = map[string]string{
	"/":           "static/index.html",
	"/index.html": "static/index.html",
	"/app.js":     "static/app.js",
	"/style.css":  "static/style.css",
}

// Handler serves embedded assets directly, without any directory redirects.
func Handler() http.Handler {
	types := map[string]string{
		".html": "text/html; charset=utf-8",
		".js":   "text/javascript; charset=utf-8",
		".css":  "text/css; charset=utf-8",
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := allowed[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			http.Error(w, "asset missing", http.StatusInternalServerError)
			return
		}
		ct := "application/octet-stream"
		for suf, t := range types {
			if strings.HasSuffix(p, suf) {
				ct = t
			}
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	})
}
