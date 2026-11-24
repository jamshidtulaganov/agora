// Command agora-mcp serves the workspace connector over Streamable HTTP.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/connectauth"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"

	"github.com/jamshidtulaganov/agora/server/internal/agoramcp"
)

func main() {
	version := os.Getenv("RENDER_GIT_COMMIT")
	if version == "" {
		version = "dev"
	}
	issuer := os.Getenv("RENDER_EXTERNAL_URL")
	if issuer == "" {
		issuer = os.Getenv("AGORA_MCP_PUBLIC_URL")
	}
	key, err := secretbox.LoadKey(connectauth.KeyEnv)
	if err != nil {
		log.Fatal(err)
	}
	// AGORA_APP_URL is where members sign in and approve the connection. It
	// defaults to the API origin, which on Render is the same web app.
	app := os.Getenv("AGORA_APP_URL")
	if app == "" {
		app = os.Getenv("AGORA_API_URL")
	}
	handler, err := agoramcp.NewMemberConnector(issuer, os.Getenv("AGORA_API_URL"), app, version, key)
	if err != nil {
		log.Fatal(err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 55 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
