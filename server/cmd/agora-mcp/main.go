// Command agora-mcp serves the workspace connector over Streamable HTTP.
package main

import (
	"log"
	"net/http"
	"os"
	"time"

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
	key, err := secretbox.LoadKey("AGORA_MCP_OAUTH_KEY")
	if err != nil {
		log.Fatal(err)
	}
	handler, err := agoramcp.NewMemberConnector(issuer, os.Getenv("AGORA_API_URL"), os.Getenv("AGORA_WORKSPACE_ID"), version, key)
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
