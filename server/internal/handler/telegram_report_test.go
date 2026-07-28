package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/telegram"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestTruncateForTelegram(t *testing.T) {
	t.Run("short text passes through untouched", func(t *testing.T) {
		in := "Weekly report\n\nBacklog grew by 12."
		if got := truncateForTelegram(in); got != in {
			t.Errorf("got %q, want the input unchanged", got)
		}
	})

	t.Run("exactly at the limit is not trimmed", func(t *testing.T) {
		in := strings.Repeat("x", telegramMessageLimit)
		if got := truncateForTelegram(in); got != in {
			t.Errorf("len %d was trimmed; the limit is inclusive", len(got))
		}
	})

	t.Run("over the limit fits within the Bot API cap", func(t *testing.T) {
		// A real report: many lines, so a clean break exists near the cut.
		in := strings.Repeat("a line of the weekly report\n", 400)
		got := truncateForTelegram(in)
		if len(got) > telegramMessageLimit {
			t.Fatalf("len %d exceeds the %d cap — Telegram would reject the whole message", len(got), telegramMessageLimit)
		}
		if !strings.Contains(got, "trimmed") {
			t.Error("a cut message must say so, or a partial report reads as complete")
		}
		if strings.HasSuffix(strings.TrimSpace(strings.Split(got, "…")[0]), "of the weekly rep") {
			t.Error("cut mid-word despite a nearby newline")
		}
	})

	t.Run("no newlines near the cut still fits", func(t *testing.T) {
		// Pathological: one enormous line. The newline-seek must not fire and
		// must not blow the budget either.
		in := strings.Repeat("x", telegramMessageLimit*2)
		got := truncateForTelegram(in)
		if len(got) > telegramMessageLimit {
			t.Fatalf("len %d exceeds cap on a newline-free report", len(got))
		}
	})

	t.Run("multibyte text stays within the byte cap", func(t *testing.T) {
		// Reports are written in the issue's language — Cyrillic and Uzbek text
		// is multi-byte, and the Bot API limit that matters here is on length.
		in := strings.Repeat("Отчёт по задачам за неделю\n", 400)
		got := truncateForTelegram(in)
		if len(got) > telegramMessageLimit {
			t.Fatalf("len %d exceeds cap for multibyte text", len(got))
		}
	})
}

// Stranded-output recovery: a completed run whose agent left no postable
// comment is a report that failed to reach anyone, not deliberate silence.
func TestAutopilotRunOutput(t *testing.T) {
	t.Run("recovers the agent's output", func(t *testing.T) {
		got := autopilotRunOutput([]byte(`{"output":"Haftalik hisobot\n\nBacklog +41."}`))
		if got != "Haftalik hisobot\n\nBacklog +41." {
			t.Errorf("got %q", got)
		}
	})

	t.Run("unescapes literal backslash-n the same way the comment path does", func(t *testing.T) {
		got := autopilotRunOutput([]byte(`{"output":"line one\\nline two"}`))
		if got != "line one\nline two" {
			t.Errorf("got %q, want a real newline", got)
		}
	})

	t.Run("nothing to recover yields empty, not garbage", func(t *testing.T) {
		for _, in := range [][]byte{nil, {}, []byte(`{}`), []byte(`not json`), []byte(`{"output":"   "}`)} {
			if got := autopilotRunOutput(in); got != "" {
				t.Errorf("input %q recovered %q, want empty", in, got)
			}
		}
	})
}

func TestChooseAutopilotDestinationKeepsBotAndChatPaired(t *testing.T) {
	agentBot := telegram.NewBotClient("agent-token")
	platformBot := telegram.NewBotClient("platform-token")

	bot, chat := chooseAutopilotDestination(agentBot, "-100-agent", false, platformBot, "")
	if bot != agentBot || chat != "-100-agent" {
		t.Fatalf("complete agent destination = (%p, %q), want agent pair", bot, chat)
	}

	bot, chat = chooseAutopilotDestination(agentBot, "-100-agent", true, platformBot, "-100-project")
	if bot != agentBot || chat != "-100-project" {
		t.Fatalf("reachable project override = (%p, %q), want agent/project pair", bot, chat)
	}

	bot, chat = chooseAutopilotDestination(agentBot, "-100-agent", false, platformBot, "-100-project")
	if bot != platformBot || chat != "-100-project" {
		t.Fatalf("agent without chat = (%p, %q), want platform pair", bot, chat)
	}

	bot, chat = chooseAutopilotDestination(agentBot, "-100-agent", false, nil, "-100-project")
	if bot != nil || chat != "" {
		t.Fatalf("chat without its bot = (%p, %q), want no destination", bot, chat)
	}
}

func TestRunOnlyAutopilotReportUsesRunResult(t *testing.T) {
	h := &Handler{}
	run := db.AutopilotRun{Result: []byte(`{"output":"run-only weekly report"}`)}
	got := h.autopilotRunReportBody(context.Background(), run, db.Autopilot{})
	if got != "run-only weekly report" {
		t.Fatalf("body = %q, want the run result", got)
	}
}

func TestAutopilotSpeakerAgentResolvesSquadLeader(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := t.Context()
	var leaderID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1::uuid LIMIT 1`,
		testWorkspaceID,
	).Scan(&leaderID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}
	var squadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		VALUES ($1::uuid, $2, $3::uuid, $4::uuid)
		RETURNING id::text`,
		testWorkspaceID, "telegram destination fixture", leaderID, testUserID,
	).Scan(&squadID); err != nil {
		t.Fatalf("create squad: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1::uuid`, squadID)
	})

	got, ok := testHandler.autopilotSpeakerAgent(ctx, db.Autopilot{
		AssigneeType: "squad",
		AssigneeID:   parseUUID(squadID),
	})
	if !ok || uuidToString(got) != leaderID {
		t.Fatalf("speaker = %s, ok=%v; want leader %s", uuidToString(got), ok, leaderID)
	}
}

func TestShortReportStaysAMessage(t *testing.T) {
	// A daily sprint pulse is six lines with no table. Delivering that as an
	// .xlsx buries it: the reader downloads and opens a file to learn one
	// sentence, and then stops looking.
	pulse := "Sprint 11: 58 tadan 8 tasi yopilgan, 6 tasi muddati o'tgan.\n" +
		"Code Review 18 · Testing 4 · Need merge 2\n" +
		"Buglar: 21 ta ochiq, 7 ta yopilgan.\n"
	if replyNeedsDocument(pulse) {
		t.Fatal("a short table-free report would be sent as a spreadsheet")
	}
}

func TestTabularReportBecomesASpreadsheet(t *testing.T) {
	// The weekly report IS its tables; inline they collapse into pipe soup.
	weekly := "Backlog o'sdi.\n\n| Oy | Soni |\n|---|---|\n| Yanvar | 360 |\n"
	if !replyNeedsDocument(weekly) {
		t.Fatal("a report carrying a table would be pasted as text")
	}
}

func TestStripsAgentPreamble(t *testing.T) {
	// Both openers observed live. Instructing the model not to write them works
	// most of the time, which is exactly why the platform has to handle it: the
	// group periodically got a status line meant for the operator.
	for _, preamble := range []string{
		"Data tayyor. Hisobotni tuzaman:",
		"Autopilot run-only rejimida — platforma xabarni o'zi yuboradi. Final natija yozaman:",
	} {
		got := stripReportPreamble(preamble + "\n\n**Sprint 11** · 29-iyul\n58 vazifa\n")
		if strings.Contains(got, "tuzaman") || strings.Contains(got, "Final natija") {
			t.Errorf("preamble survived: %q", got)
		}
		if !strings.HasPrefix(got, "**Sprint 11**") {
			t.Errorf("content lost: %q", got)
		}
	}
}

func TestKeepsReportsThatSimplyStartWithAColon(t *testing.T) {
	// Narrow by construction: content that happens to end a line with a colon
	// must survive. The failure mode to prefer is a preamble slipping through,
	// never a report losing its first line.
	for _, body := range []string{
		"## Asosiy raqamlar:\n\n| a | b |\n|---|---|\n",
		"- Birinchi band:\n\n| a | b |\n|---|---|\n",
		"**Sprint 11** · 29-iyul\n58 vazifa · 8 yopilgan\n",
	} {
		if got := stripReportPreamble(body); !strings.HasPrefix(got, strings.TrimSpace(strings.Split(body, "\n")[0])) {
			t.Errorf("content was eaten:\nin:  %q\nout: %q", body, got)
		}
	}
}

func TestStripPreambleLeavesASingleBlockAlone(t *testing.T) {
	// With nothing after it, a colon line IS the report — dropping it would
	// leave the group with an empty message.
	body := "Bugun yangilik yo'q:"
	if got := stripReportPreamble(body); got != body {
		t.Fatalf("a lone line was dropped: %q", got)
	}
}
