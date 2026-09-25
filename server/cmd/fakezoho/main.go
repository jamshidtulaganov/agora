// Command fakezoho serves the in-memory Zoho stand-in (internal/integrations/
// zohofake) for local development, so the Connect Zoho flow and the read-only
// Zoho tools can be tried without a real Zoho org.
//
//	go run ./cmd/fakezoho -addr 127.0.0.1:18990
//
// Then run the backend with:
//
//	ZOHO_DYN_ACCOUNTS_BASE=http://127.0.0.1:18990
//	ZOHO_DYN_API_BASE=http://127.0.0.1:18990
//	ZOHO_DYN_DESK_BASE=http://127.0.0.1:18990
//
// and set up the workspace's Zoho connector (Settings → Integrations → Zoho)
// with Client ID 1000.FAKEZOHOCLIENT and Client secret fake-zoho-secret.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/zohofake"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18990", "listen address")
	flag.Parse()
	srv := zohofake.New("http://" + *addr)
	log.Printf("fake Zoho on http://%s (client id %s)", *addr, zohofake.ClientID)
	for _, u := range zohofake.Users {
		log.Printf("  person: %s — %s (%s)", u.Name, u.Role, u.Profile)
	}
	log.Fatal(http.ListenAndServe(*addr, srv))
}
