package lark

import (
	"encoding/json"
	"strings"
	"testing"
)

// decodeCard parses the card JSON and returns its top-level keys for assertions.
func decodeCard(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("card is not valid JSON: %v\n%s", err, raw)
	}
	return doc
}

func TestIssueNotifyCard_WithButton(t *testing.T) {
	raw, err := IssueNotifyCard("🔔 Login bug", "**Alice**\nplease look", "https://agora.example/acme/issues/abc", "Open in Agora")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := decodeCard(t, raw)

	if _, ok := doc["header"]; !ok {
		t.Error("card missing header")
	}
	elements, ok := doc["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Fatalf("card elements missing/empty: %s", raw)
	}
	// The URL must reach the button.
	if !strings.Contains(raw, "https://agora.example/acme/issues/abc") {
		t.Errorf("issue URL not embedded in card: %s", raw)
	}
	if !strings.Contains(raw, "Open in Agora") {
		t.Errorf("button label not embedded in card: %s", raw)
	}
}

func TestIssueNotifyCard_NoURL_OmitsButton(t *testing.T) {
	raw, err := IssueNotifyCard("🔔 Login bug", "some body", "", "Open in Agora")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(raw, "\"tag\":\"action\"") || strings.Contains(raw, "\"button\"") {
		t.Errorf("blank URL must omit the action/button block: %s", raw)
	}
	// Still a valid, non-empty card.
	doc := decodeCard(t, raw)
	if elements, ok := doc["elements"].([]any); !ok || len(elements) == 0 {
		t.Errorf("card must still have body elements without a URL: %s", raw)
	}
}

func TestIssueNotifyCard_EmptyBodyAndURL_StillValid(t *testing.T) {
	// Lark rejects an empty elements array — the headline must back-fill.
	raw, err := IssueNotifyCard("🔔 Heads up", "", "", "Open in Agora")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := decodeCard(t, raw)
	elements, ok := doc["elements"].([]any)
	if !ok || len(elements) == 0 {
		t.Errorf("elements must never be empty: %s", raw)
	}
}
