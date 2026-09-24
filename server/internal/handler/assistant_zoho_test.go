package handler

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// toolRecordingChat replays replies and records the system prompt and the
// tool names each model call was offered.
type toolRecordingChat struct {
	mu      sync.Mutex
	replies []llm.Message
	systems []string
	offered [][]string
	last    []llm.Message
}

func (c *toolRecordingChat) CompleteWithTools(ctx context.Context, model string, msgs []llm.Message, tools []llm.Tool) (llm.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	c.offered = append(c.offered, names)
	if len(msgs) > 0 {
		c.systems = append(c.systems, msgs[0].Content)
	}
	c.last = append([]llm.Message(nil), msgs...)
	if len(c.replies) == 0 {
		return llm.Message{}, errors.New("script exhausted")
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	return reply, nil
}

func runAssistantWith(t *testing.T, user, question string, chat *toolRecordingChat) {
	t.Helper()
	sessionID := newAssistantTestSession(t, user)
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO assistant_message (session_id, role, content) VALUES ($1, 'user', $2)`, sessionID, question); err != nil {
		t.Fatalf("seed user message: %v", err)
	}
	svc := assistant.NewService(testHandler.Queries, events.New(), func() (llm.ToolChat, string, error) {
		return chat, "test-model", nil
	})
	svc.Exec = testHandler
	svc.Integrations = testHandler.assistantIntegrations
	svc.Run(context.Background(), sessionID, "run-zoho-"+user, user)
}

func offeredTool(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestAssistantReadsZohoAsTheChattingPerson(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fake := startFakeZoho(t)
	user := newAssistantTestUser(t, "assistant-zoho@agora.dev")
	connectZohoAs(t, fake, user, "shohruh")

	chat := &toolRecordingChat{replies: []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{
			ID: "call_z", Name: "zoho_crm_search",
			Arguments: `{"coql":"SELECT Deal_Name, Stage FROM Deals LIMIT 50"}`,
		}}},
		{Role: "assistant", Content: "You have two open deals."},
	}}
	runAssistantWith(t, user, "what are my deals?", chat)

	chat.mu.Lock()
	defer chat.mu.Unlock()
	if len(chat.offered) != 2 || !offeredTool(chat.offered[0], "zoho_crm_search") || !offeredTool(chat.offered[0], "zoho_desk_list_tickets") {
		t.Fatalf("zoho tools not offered: %v", chat.offered)
	}
	for _, name := range chat.offered[0] {
		if strings.HasPrefix(name, "zoho_") && (strings.Contains(name, "create") || strings.Contains(name, "update")) {
			t.Fatalf("a zoho write tool was offered: %s", name)
		}
	}
	if !strings.Contains(chat.systems[0], "ZOHO (read-only)") || !strings.Contains(chat.systems[0], "Collections Agent") {
		t.Fatalf("system prompt lacks the Zoho note: %s", chat.systems[0])
	}
	toolResult := chat.last[len(chat.last)-1]
	if toolResult.Role != "tool" || !strings.Contains(toolResult.Content, "Trans Union settlement") ||
		strings.Contains(toolResult.Content, "Write-off review") {
		t.Fatalf("tool result should hold only this person's deals: %+v", toolResult)
	}

	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM zoho_call_log WHERE user_id = $1 AND source = 'assistant' AND tool = 'zoho_crm_search' AND record_count = 2`, user,
	).Scan(&n); err != nil || n != 1 {
		t.Fatalf("assistant call not audited: n=%d err=%v", n, err)
	}
}

func TestAssistantWithoutZohoOnlyGetsHowToConnect(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	startFakeZoho(t)
	user := newAssistantTestUser(t, "assistant-no-zoho@agora.dev")

	chat := &toolRecordingChat{replies: []llm.Message{{Role: "assistant", Content: "Connect Zoho first."}}}
	runAssistantWith(t, user, "show my tickets", chat)

	chat.mu.Lock()
	defer chat.mu.Unlock()
	for _, name := range chat.offered[0] {
		if strings.HasPrefix(name, "zoho_") {
			t.Fatalf("zoho tool %s offered to someone who hasn't connected Zoho", name)
		}
	}
	if !strings.Contains(chat.systems[0], "hasn't connected Zoho") {
		t.Fatalf("system prompt lacks the connect hint: %s", chat.systems[0])
	}

	// A hallucinated call still can't reach anyone's Zoho.
	_, err := testHandler.Execute(context.Background(), user, "", "zoho_desk_list_tickets", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "hasn't connected Zoho") {
		t.Fatalf("expected not-connected error, got %v", err)
	}
}

func TestZohoToolSpecsAreClosedAndReadOnly(t *testing.T) {
	specs := assistant.ZohoToolSpecs()
	if len(specs) == 0 {
		t.Fatal("no zoho tool specs")
	}
	for _, s := range specs {
		var schema map[string]any
		if err := json.Unmarshal(s.Parameters, &schema); err != nil || schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("tool %s schema not closed: %s", s.Name, s.Parameters)
		}
		if !assistant.IsZohoTool(s.Name) || assistant.IsMutating(s.Name) || assistant.RequiresConfirmation(s.Name) {
			t.Fatalf("tool %s misclassified", s.Name)
		}
	}
	for _, s := range assistant.ToolSpecs() {
		if assistant.IsZohoTool(s.Name) {
			t.Fatalf("zoho tool %s must not be in the static catalog", s.Name)
		}
	}
}
