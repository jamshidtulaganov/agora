// Command larkcard posts a single interactive card with a request-button to a
// Lark chat, to PROVE the card.action.trigger long-conn path: tap the button,
// then watch the backend logs for "lark card action received". One-off test
// harness; delete with cmd/larkfix once card actions ship.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/integrations/lark"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func main() {
	wsID := flag.String("ws", "356e2301-64fd-440b-ab68-7fcfa76088b1", "workspace UUID (sd-main)")
	agentID := flag.String("agent", "398e10be-5d60-4d2e-8ae2-83a7254ca9b9", "agent UUID (sd-bridge-lead)")
	chatID := flag.String("chat", "oc_f805269dc51d86589102492840d93519", "Lark chat id (the bound DM)")
	issueID := flag.String("issue", "", "issue UUID the buttons act on (required for a real mutation)")
	flag.Parse()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()
	queries := db.New(pool)

	key, err := secretbox.LoadKey("AGORA_LARK_SECRET_KEY")
	if err != nil {
		log.Fatalf("load key: %v", err)
	}
	box, err := secretbox.New(key)
	if err != nil {
		log.Fatalf("secretbox: %v", err)
	}
	svc, err := lark.NewInstallationService(queries, box)
	if err != nil {
		log.Fatalf("install service: %v", err)
	}

	wsUUID, err := util.ParseUUID(*wsID)
	if err != nil {
		log.Fatalf("bad ws uuid: %v", err)
	}
	agentUUID, err := util.ParseUUID(*agentID)
	if err != nil {
		log.Fatalf("bad agent uuid: %v", err)
	}
	inst, err := queries.GetLarkInstallationByAgent(ctx, db.GetLarkInstallationByAgentParams{
		WorkspaceID: wsUUID,
		AgentID:     agentUUID,
	})
	if err != nil {
		log.Fatalf("get installation: %v", err)
	}
	secret, err := svc.DecryptAppSecret(inst)
	if err != nil {
		log.Fatalf("decrypt: %v", err)
	}
	creds := lark.InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
		TenantKey: inst.TenantKey.String,
		Region:    lark.RegionOrDefault(inst.Region),
	}

	api := lark.NewHTTPAPIClient(lark.HTTPClientConfig{})
	card, err := lark.IssueActionCard("TEST", "Card action test (status / assign / QA)", *issueID, "")
	if err != nil {
		log.Fatalf("build card: %v", err)
	}
	msgID, err := api.SendInteractiveCard(ctx, lark.SendCardParams{
		InstallationID: creds,
		ChatID:         lark.ChatID(*chatID),
		CardJSON:       card,
	})
	if err != nil {
		log.Fatalf("send card: %v", err)
	}
	fmt.Printf("OK sent card message_id=%s to chat=%s\n", msgID, *chatID)
	fmt.Println("Now TAP a button (status / Assign to me / QA) in Lark, then check:")
	fmt.Println("  docker logs multica-backend-1 | grep -i 'card action'")
}
