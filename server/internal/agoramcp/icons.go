package agoramcp

import (
	"embed"
	"net/http"
)

// Brand icons (the "assembly" mark), copied from apps/web/public. Claude and ChatGPT show a
// connector's icon from its site, so the connector serves Agora's logo at the
// usual favicon paths.
//
//go:embed assets/favicon-32x32.png assets/icon-192.png assets/icon-512.png assets/favicon.svg
var icons embed.FS

var iconRoutes = map[string]struct{ file, mime string }{
	"/favicon.ico":          {"assets/favicon-32x32.png", "image/png"},
	"/favicon-32x32.png":    {"assets/favicon-32x32.png", "image/png"},
	"/favicon.svg":          {"assets/favicon.svg", "image/svg+xml"},
	"/icon-192.png":         {"assets/icon-192.png", "image/png"},
	"/icon-512.png":         {"assets/icon-512.png", "image/png"},
	"/apple-touch-icon.png": {"assets/icon-192.png", "image/png"},
}

func registerIcons(mux *http.ServeMux) {
	for path, icon := range iconRoutes {
		data, err := icons.ReadFile(icon.file)
		if err != nil {
			panic("agoramcp: missing embedded icon " + icon.file)
		}
		mime := icon.mime
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Cache-Control", "public, max-age=86400")
			_, _ = w.Write(data)
		})
	}
}
