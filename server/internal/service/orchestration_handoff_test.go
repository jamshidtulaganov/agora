package service

import (
	"strings"
	"testing"
)

func TestParseOrchestrationHandoff(t *testing.T) {
	output := "Done.\n```agora-handoff\n" + `{
  "schema_version": 1,
  "stage": "release",
  "outcome": "completed",
  "summary": "Implemented the API contract",
  "decisions": ["Use cursor pagination"],
  "contracts": ["GET /items returns next_cursor"],
  "artifacts": [{"kind":"commit","ref":"abc123"}],
  "verification": [{"name":"go test ./...","status":"passed"}],
  "findings": [],
  "risks": ["Old clients ignore next_cursor"],
  "blockers": [],
  "next_actions": ["Verify UI pagination"]
}` + "\n```"

	handoff, ok := ParseOrchestrationHandoff("dev", output)
	if !ok {
		t.Fatal("expected handoff to parse")
	}
	if handoff.Stage != "dev" {
		t.Fatalf("server stage must win, got %q", handoff.Stage)
	}
	if handoff.Outcome != "completed" || handoff.Summary != "Implemented the API contract" {
		t.Fatalf("unexpected handoff: %#v", handoff)
	}
	if len(handoff.Verification) != 1 || handoff.Verification[0].Status != "passed" {
		t.Fatalf("verification was not retained: %#v", handoff.Verification)
	}
}

func TestParseOrchestrationHandoffWaitingInputRequiresQuestion(t *testing.T) {
	withoutQuestion := "```agora-handoff\n" + `{"outcome":"waiting_input","summary":"Need a decision"}` + "\n```"
	if _, ok := ParseOrchestrationHandoff("plan", withoutQuestion); ok {
		t.Fatal("waiting_input without a question must be rejected")
	}

	withQuestion := "```agora-handoff\n" + `{"outcome":"waiting_input","summary":"Need a decision","question":{"prompt":"Which API version?","target":"human"}}` + "\n```"
	handoff, ok := ParseOrchestrationHandoff("plan", withQuestion)
	if !ok || handoff.Question == nil || !handoff.Question.Blocking {
		t.Fatalf("expected blocking question, got %#v, ok=%v", handoff.Question, ok)
	}
}

func TestParseOrchestrationHandoffBlockedRequiresBlocker(t *testing.T) {
	invalid := "```agora-handoff\n" + `{"outcome":"blocked","summary":"Cannot continue"}` + "\n```"
	if _, ok := ParseOrchestrationHandoff("integration", invalid); ok {
		t.Fatal("blocked handoff without blockers must be rejected")
	}
}

func TestNormalizeOrchestrationHandoffLegacyFallback(t *testing.T) {
	input := strings.Repeat("x", 9000)
	handoff, parsed := NormalizeOrchestrationHandoff("qa", input)
	if parsed || !handoff.Legacy {
		t.Fatalf("expected legacy fallback, got parsed=%v handoff=%#v", parsed, handoff)
	}
	if handoff.Stage != "qa" || handoff.Outcome != "completed" {
		t.Fatalf("unexpected fallback: %#v", handoff)
	}
	if handoff.Artifacts == nil || handoff.Verification == nil || handoff.Decisions == nil {
		t.Fatalf("handoff collections must serialize as arrays, got %#v", handoff)
	}
	if len([]rune(handoff.Summary)) != 8001 {
		t.Fatalf("summary should be capped with ellipsis, got %d runes", len([]rune(handoff.Summary)))
	}
}

func TestNormalizeOrchestrationHandoffDefaultsNonGateVerdict(t *testing.T) {
	handoff, parsed := NormalizeOrchestrationHandoff("plan", "Plan complete")
	if parsed || handoff.Verdict != "not_applicable" {
		t.Fatalf("non-gate handoff verdict = %q, parsed=%v", handoff.Verdict, parsed)
	}
}

func TestParseOrchestrationHandoffRoutesQuestionsToHumanConsumer(t *testing.T) {
	input := "```agora-handoff\n" + `{"outcome":"waiting_input","summary":"Need a decision","question":{"prompt":"Which contract?","target":"agent","target_id":"foreign-agent"}}` + "\n```"
	handoff, ok := ParseOrchestrationHandoff("dev", input)
	if !ok || handoff.Question == nil {
		t.Fatalf("expected parsed question, got %#v ok=%v", handoff, ok)
	}
	if handoff.Question.Target != "human" || handoff.Question.TargetID != "" {
		t.Fatalf("question target has no automatic consumer: %#v", handoff.Question)
	}
}

func TestParseOrchestrationHandoffAcceptsRichStringListItems(t *testing.T) {
	output := "```agora-handoff\n" + `{
  "schema_version": 1,
  "stage": "review",
  "outcome": "completed",
  "verdict": "pass",
  "summary": "Contract is correct",
  "decisions": [{"decision":"Keep the additive field"}],
  "contracts": [{"name":"invoice producer","effect":"read"}],
  "artifacts": [],
  "verification": [{"name":"static review","status":"passed"}],
  "findings": [{"severity":"low","details":"Spanish fallback remains"}],
  "risks": [{"risk":"Dependencies unavailable"}],
  "blockers": [],
  "next_actions": [{"owner":"qa","action":"Run the authenticated smoke"}]
}` + "\n```"
	handoff, ok := ParseOrchestrationHandoff("review", output)
	if !ok || handoff.Verdict != "pass" {
		t.Fatalf("rich handoff should retain its verdict: ok=%v handoff=%#v", ok, handoff)
	}
	if len(handoff.Decisions) != 1 || handoff.Decisions[0] != "Keep the additive field" {
		t.Fatalf("decision object was not normalized: %#v", handoff.Decisions)
	}
	if len(handoff.Findings) != 1 || handoff.Findings[0] != "Spanish fallback remains" {
		t.Fatalf("finding object was not normalized: %#v", handoff.Findings)
	}
	if len(handoff.NextActions) != 1 || handoff.NextActions[0] != "Run the authenticated smoke" {
		t.Fatalf("action object was not normalized: %#v", handoff.NextActions)
	}
}
