// Package webui embebe el frontend React+shadcn ya COMPILADO. Node solo se usa
// para construirlo (en harness-installer/templates/ui/web) y copiar dist/ aquí;
// el daemon final no necesita Node — sirve estáticos desde el binario.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var dist embed.FS

// MIME por extensión — igual de acotado que el panel de Python.
var mimes = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript",
	".css": "text/css", ".svg": "image/svg+xml", ".woff2": "font/woff2",
	".png": "image/png", ".ico": "image/x-icon", ".map": "application/json",
}

// Handler sirve el build. index.html en "/", y /assets/* directo. Fuera de
// dist/ → 404 (el embed ya lo garantiza: no hay ".." que escape).
// opToken se inyecta en el HTML (plano de operar, ADR-0010).
func Handler(opToken string) http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		ext := ""
		if i := strings.LastIndex(p, "."); i >= 0 {
			ext = p[i:]
		}
		ct := mimes[ext]
		if ct == "" {
			ct = "application/octet-stream"
		}
		rw.Header().Set("Content-Type", ct)
		rw.Header().Set("Cache-Control", "no-store")
		if ext == ".html" {
			b = []byte(strings.ReplaceAll(string(b), "__OP_TOKEN__", opToken))
		}
		_, _ = rw.Write(b)
	})
}
