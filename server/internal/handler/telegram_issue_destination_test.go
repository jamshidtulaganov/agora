package handler

import (
	"context"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/telegram"
)

// TestChooseAutomationDestinationOrder documents the resolution ORDER as pure
// logic, mirroring what resolveIssueTelegramDestination does with real rows. The
// order is the whole point of the change: a workspace binds groups in the UI, so a
// static env chat id must be the LAST resort, not the requirement.
func TestChooseAutomationDestinationOrder(t *testing.T) {
	agentBot := telegram.NewBotClient("agent-token")
	platformBot := telegram.NewBotClient("platform-token")

	cases := []struct {
		name        string
		explicit    string
		agentBot    *telegram.BotClient
		agentChat   string
		agentReach  bool
		platform    *telegram.BotClient
		projectChat string
		wantChat    string
		wantVia     string
		wantOK      bool
	}{
		{
			name:     "explicit room, agent bot is in it → posts as the agent",
			explicit: "-1001", agentBot: agentBot, agentChat: "-1002", agentReach: true,
			platform: platformBot, wantChat: "-1001", wantVia: "agent", wantOK: true,
		},
		{
			name:     "explicit room, agent bot is NOT in it → platform bot",
			explicit: "-1001", agentBot: agentBot, agentChat: "-1002", agentReach: false,
			platform: platformBot, wantChat: "-1001", wantVia: "platform", wantOK: true,
		},
		{
			name:     "no explicit room → the agent's own bound group",
			agentBot: agentBot, agentChat: "-1002", platform: platformBot, projectChat: "-1009",
			wantChat: "-1002", wantVia: "agent", wantOK: true,
		},
		{
			name:     "agent has a bot but no bound group → configured project room",
			agentBot: agentBot, agentChat: "", platform: platformBot, projectChat: "-1009",
			wantChat: "-1009", wantVia: "platform", wantOK: true,
		},
		{
			name:     "nothing bound and nothing configured → no destination",
			platform: platformBot,
			wantOK:   false,
		},
		{
			name:      "no platform bot and no agent group → no destination",
			agentBot:  agentBot,
			agentChat: "",
			wantOK:    false,
		},
	}

	for _, c := range cases {
		got, ok := chooseIssueTelegramDestination(
			c.explicit, c.agentBot, c.agentChat, c.agentReach, c.platform, c.projectChat,
		)
		if ok != c.wantOK {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.chatID != c.wantChat || got.via != c.wantVia {
			t.Errorf("%s: got chat %q via %q, want chat %q via %q", c.name, got.chatID, got.via, c.wantChat, c.wantVia)
		}
	}
}

// TestResolveIssueTelegramDestinationNoBinding: with no installation and no
// project override, resolution reports "nothing" rather than erroring — a rule whose
// point is to move a task must not fail because no room is bound yet.
func TestResolveIssueTelegramDestinationNoBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Setenv("AGORA_TELEGRAM_REPORT_CHAT_ID", "")
	ctx := context.Background()
	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	if _, ok := testHandler.resolveIssueTelegramDestination(ctx, issue, ""); ok {
		t.Error("an issue with no bound group and no override must resolve no destination")
	}
}
