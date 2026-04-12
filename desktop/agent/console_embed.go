package main

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Console SPA static bundle. Populated by the build pipeline
// (cd web && npm run build && cp -r out/* ../desktop/agent/console_static/).
// Until then, this directory contains only .keep — the handler falls back
// to a minimal HTML page pointing at yaver.io/dashboard.
//
//go:embed console_static
var consoleStaticFS embed.FS

// mountConsoleEmbed wires GET /app/* and GET /app to serve the embedded
// console SPA. Router config / deep links are handled by the SPA — non-file
// paths fall through to /app/index.html.
func (s *HTTPServer) mountConsoleEmbed(mux *http.ServeMux) {
	sub, err := fs.Sub(consoleStaticFS, "console_static")
	if err != nil {
		return
	}
	fileServer := http.FileServer(http.FS(sub))

	mux.HandleFunc("/app/", func(w http.ResponseWriter, r *http.Request) {
		// Strip /app prefix before looking up the file.
		path := strings.TrimPrefix(r.URL.Path, "/app/")
		if path == "" || path == "index.html" {
			serveConsoleIndex(w, sub)
			return
		}
		// Try the exact file.
		if f, err := sub.Open(path); err == nil {
			f.Close()
			http.StripPrefix("/app", fileServer).ServeHTTP(w, r)
			return
		}
		// SPA fallback — any non-file route serves index.html.
		serveConsoleIndex(w, sub)
	})
	mux.HandleFunc("/app", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusTemporaryRedirect)
	})
}

func serveConsoleIndex(w http.ResponseWriter, sub fs.FS) {
	if f, err := sub.Open("index.html"); err == nil {
		defer f.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fsCopy(w, f)
		return
	}
	// Fallback page when the SPA hasn't been built into the binary yet.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Yaver Console</title>
<style>body{font-family:ui-monospace,monospace;background:#0b0d10;color:#d1d5db;padding:2rem;max-width:640px;margin:0 auto}a{color:#818cf8}</style>
</head><body>
<h1>Yaver Console</h1>
<p>Agent is running. The in-agent SPA bundle hasn't been populated yet — run:</p>
<pre>cd web && npm run build && cp -r out/* ../desktop/agent/console_static/ && cd ../desktop/agent && go build</pre>
<p>In the meantime, open the hosted dashboard at <a href="https://yaver.io/dashboard">yaver.io/dashboard</a> and pick this device.</p>
</body></html>`))
}

func fsCopy(w http.ResponseWriter, src fs.File) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			nw, werr := w.Write(buf[:n])
			total += int64(nw)
			if werr != nil {
				return total, werr
			}
		}
		if err != nil {
			if err.Error() == "EOF" {
				return total, nil
			}
			return total, err
		}
	}
}
