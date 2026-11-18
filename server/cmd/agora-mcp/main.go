// Command agora-mcp serves the workspace connector over Streamable HTTP.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/agoramcp"
)

func main() {
	version := os.Getenv("RENDER_GIT_COMMIT")
	if version == "" {
		version = "dev"
	}
	handler, err := agoramcp.NewRemoteHandler(os.Getenv("AGORA_API_URL"), os.Getenv("AGORA_WORKSPACE_ID"), version)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 55 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
