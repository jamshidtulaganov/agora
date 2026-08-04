// Command larkfix manually registers a Lark Bot installation from
// already-minted credentials, recovering from a device-flow that created
// the Bot on Lark's side but never wrote a lark_installation row (the
// intl "completes but no row" failure). One-off; delete after use.
//
// Reads LARK_APP_ID / LARK_APP_SECRET from the environment (never flags, so
// the secret stays out of the process arg list). Resolves bot_open_id via
// the Lark API, encrypts app_secret through the same secretbox the server
// uses, and Upserts the row for the chosen workspace/agent.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/lark"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func main() {
	wsID := flag.String("ws", "356e2301-64fd-440b-ab68-7fcfa76088b1", "workspace UUID (sd-main)")
	agentID := flag.String("agent", "398e10be-5d60-4d2e-8ae2-83a7254ca9b9", "agent UUID (sd-bridge-lead)")
	userID := flag.String("user", "ba5d054a-8cc9-4258-846a-5aafbe776cba", "installer user UUID")
	flag.Parse()

	appID := os.Getenv("LARK_APP_ID")
	appSecret := os.Getenv("LARK_APP_SECRET")
	if appID == "" || appSecret == "" {
		log.Fatal("LARK_APP_ID and LARK_APP_SECRET must be set in env")
	}

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

	api := lark.NewHTTPAPIClient(lark.HTTPClientConfig{})
	creds := lark.InstallationCredentials{AppID: appID, AppSecret: appSecret, Region: lark.RegionLark}

	bot, err := api.GetBotInfo(ctx, creds)
	if err != nil {
		log.Fatalf("GetBotInfo (check app_id/secret + that it's a Lark intl app): %v", err)
	}
	fmt.Printf("bot_open_id=%s union_id=%s\n", bot.OpenID, bot.UnionID)
	if bot.OpenID == "" {
		log.Fatal("GetBotInfo returned empty open_id")
	}

	wsUUID, err := util.ParseUUID(*wsID)
	must(err, "ws uuid")
	agentUUID, err := util.ParseUUID(*agentID)
	must(err, "agent uuid")
	userUUID, err := util.ParseUUID(*userID)
	must(err, "user uuid")

	inst, err := svc.Upsert(ctx, lark.InstallationParams{
		WorkspaceID:     wsUUID,
		AgentID:         agentUUID,
		AppID:           appID,
		AppSecret:       appSecret,
		BotOpenID:       string(bot.OpenID),
		InstallerUserID: userUUID,
		Region:          lark.RegionLark,
	})
	if err != nil {
		log.Fatalf("upsert: %v", err)
	}
	fmt.Printf("OK installation id=%x status=%s region=%s app_id=%s\n",
		inst.ID.Bytes, inst.Status, inst.Region, inst.AppID)
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}
