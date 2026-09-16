package assistant

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

func TestRepairToolHistoryAfterInterruptedMutation(t *testing.T) {
	request := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "first", Name: "create_issue", Arguments: "{}"},
		{ID: "second", Name: "comment_issue", Arguments: "{}"},
	}}
	completed := llm.Message{Role: "tool", ToolCallID: "first", Content: `{"identifier":"TEST-1"}`}
	next := llm.Message{Role: "user", Content: "What happened?"}
	got := repairToolHistory([]llm.Message{request, completed, next})
	if len(got) != 4 || !reflect.DeepEqual(got[0], request) || !reflect.DeepEqual(got[1], completed) || !reflect.DeepEqual(got[3], next) {
		t.Fatalf("did not retain the completed action and next user turn: %+v", got)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "second" || !strings.Contains(got[2].Content, "effects are unknown") {
		t.Fatalf("missing conservative result for interrupted action: %+v", got[2])
	}
	if again := repairToolHistory(got); !reflect.DeepEqual(again, got) {
		t.Fatalf("repair should be idempotent: %+v", again)
	}
}

func TestRepairToolHistoryDropsOrphansAndPreservesCompleteExchange(t *testing.T) {
	request := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "read", Name: "get_issue", Arguments: "{}"}}}
	result := llm.Message{Role: "tool", ToolCallID: "read", Content: `{"title":"Existing issue"}`}
	orphan := llm.Message{Role: "tool", ToolCallID: "missing", Content: "unrelated"}
	next := llm.Message{Role: "user", Content: "Thanks"}
	got := repairToolHistory([]llm.Message{orphan, request, result, result, orphan, next, orphan})
	want := []llm.Message{request, result, next}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected repaired history: %+v", got)
	}
}
