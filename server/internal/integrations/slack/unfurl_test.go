package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// blockJSON flattens a block into one searchable string, so a test can assert
// what a reader would see without hand-walking the Block Kit shape.
func blockJSON(b Block) string {
	raw, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	return string(raw)
}

func blocksJSON(blocks []Block) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, blockJSON(b))
	}
	return strings.Join(parts, "\n")
}

func TestIssueUnfurlBlocks_Shape(t *testing.T) {
	blocks := IssueUnfurlBlocks(IssueUnfurl{
		Identifier:   "MUL-123",
		Title:        "Retry the failed payment webhook",
		URL:          "https://app.agora.dev/mobile/issues/MUL-123",
		Status:       "in_progress",
		Priority:     "high",
		Assignee:     "Dilshod",
		AssigneeKind: AssigneeMember,
		Project:      "Mobile",
	})

	if len(blocks) != 3 {
		t.Fatalf("expected header + context + actions, got %d blocks: %s", len(blocks), blocksJSON(blocks))
	}
	if blocks[0]["type"] != "header" || blocks[1]["type"] != "context" || blocks[2]["type"] != "actions" {
		t.Fatalf("unexpected block order: %s", blocksJSON(blocks))
	}

	all := blocksJSON(blocks)
	for _, want := range []string{
		"MUL-123 · Retry the failed payment webhook",
		"In progress", // humanised, not the raw token
		"High",
		"Dilshod",
		"Mobile",
		"https://app.agora.dev/mobile/issues/MUL-123",
		"Open in Agora",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("card is missing %q: %s", want, all)
		}
	}
	if strings.Contains(all, "in_progress") {
		t.Fatalf("raw status token leaked into the card: %s", all)
	}
}

// The noise/leak posture, as a test: an unfurl renders to everyone in the
// channel — including people who are not Agora users — so the description is
// not on the card, and adding it is a product decision rather than a refactor.
func TestIssueUnfurlBlocksCarryNoDescription(t *testing.T) {
	// The struct has no Description field, so the only way a body could reach
	// the card is through the title. Assert the fields we DO render are the
	// whole set.
	blocks := IssueUnfurlBlocks(IssueUnfurl{
		Identifier: "MUL-9",
		Title:      "Short title",
		Status:     "todo",
	})
	all := blocksJSON(blocks)
	for _, forbidden := range []string{"section", "markdown"} {
		if strings.Contains(all, `"type":"`+forbidden+`"`) {
			t.Fatalf("a %s block would be a place for a body to live: %s", forbidden, all)
		}
	}
}

// An agent assignee stays visually distinct in Slack, the way it does on every
// other Agora surface.
func TestIssueUnfurlBlocksMarkAgentAndSquadAssignees(t *testing.T) {
	agent := blocksJSON(IssueUnfurlBlocks(IssueUnfurl{
		Identifier: "MUL-1", Title: "t", Assignee: "mobile-dev", AssigneeKind: AssigneeAgent,
	}))
	if !strings.Contains(agent, ":robot_face: mobile-dev") {
		t.Fatalf("agent assignee not marked: %s", agent)
	}
	squad := blocksJSON(IssueUnfurlBlocks(IssueUnfurl{
		Identifier: "MUL-1", Title: "t", Assignee: "Platform", AssigneeKind: AssigneeSquad,
	}))
	if !strings.Contains(squad, ":busts_in_silhouette: Platform") {
		t.Fatalf("squad assignee not marked: %s", squad)
	}
	human := blocksJSON(IssueUnfurlBlocks(IssueUnfurl{
		Identifier: "MUL-1", Title: "t", Assignee: "Dilshod", AssigneeKind: AssigneeMember,
	}))
	if strings.Contains(human, ":robot_face:") {
		t.Fatalf("a human must not be marked as an agent: %s", human)
	}
}

func TestIssueUnfurlBlocksDegradeGracefully(t *testing.T) {
	t.Run("no facts at all is still a card", func(t *testing.T) {
		blocks := IssueUnfurlBlocks(IssueUnfurl{Identifier: "MUL-1"})
		if len(blocks) != 1 || blocks[0]["type"] != "header" {
			t.Fatalf("expected a lone header, got %s", blocksJSON(blocks))
		}
	})
	t.Run("nothing to identify the issue with is no card", func(t *testing.T) {
		if blocks := IssueUnfurlBlocks(IssueUnfurl{Status: "todo"}); blocks != nil {
			t.Fatalf("expected no blocks, got %s", blocksJSON(blocks))
		}
	})
	t.Run("archived is said out loud", func(t *testing.T) {
		all := blocksJSON(IssueUnfurlBlocks(IssueUnfurl{Identifier: "MUL-1", Title: "t", Archived: true}))
		if !strings.Contains(all, "Archived") {
			t.Fatalf("an archived issue must not read as current work: %s", all)
		}
	})
	t.Run("an over-long title is truncated, not rejected", func(t *testing.T) {
		blocks := IssueUnfurlBlocks(IssueUnfurl{Identifier: "MUL-1", Title: strings.Repeat("x", 500)})
		text, _ := blocks[0]["text"].(map[string]any)
		header, _ := text["text"].(string)
		if len([]rune(header)) > MaxHeaderChars {
			t.Fatalf("header is %d runes, over Slack's %d cap", len([]rune(header)), MaxHeaderChars)
		}
	})
}

func TestProjectUnfurlBlocks(t *testing.T) {
	all := blocksJSON(ProjectUnfurlBlocks(ProjectUnfurl{
		Name:   "Mobile",
		Status: "in_progress",
		URL:    "https://app.agora.dev/mobile/projects/6f1a3c22-0000-4000-8000-00000000abcd",
	}))
	for _, want := range []string{"Mobile", "In progress", "Open in Agora"} {
		if !strings.Contains(all, want) {
			t.Fatalf("project card missing %q: %s", want, all)
		}
	}
	if blocks := ProjectUnfurlBlocks(ProjectUnfurl{Status: "planned"}); blocks != nil {
		t.Fatal("a nameless project must not unfurl")
	}
}

func TestHumanizeToken(t *testing.T) {
	cases := map[string]string{
		"in_progress":      "In progress",
		"in-review":        "In review",
		"urgent":           "Urgent",
		"":                 "",
		"   ":              "",
		"some_new_status":  "Some new status", // enum drift downgrades, never crashes
		"ALREADY Capital":  "ALREADY Capital",
		"double__underbar": "Double underbar",
	}
	for in, want := range cases {
		if got := HumanizeToken(in); got != want {
			t.Fatalf("HumanizeToken(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// chat.unfurl
// ---------------------------------------------------------------------------

func TestUnfurl_SendsBlocksKeyedByURL(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})

	link := "https://app.agora.dev/mobile/issues/MUL-1"
	if _, err := c.Unfurl(context.Background(), "xoxb-token", UnfurlRequest{
		Channel: "C123",
		TS:      "1735689600.000100",
		Unfurls: map[string]Unfurl{link: {Blocks: IssueUnfurlBlocks(IssueUnfurl{Identifier: "MUL-1", Title: "t"})}},
	}); err != nil {
		t.Fatalf("Unfurl: %v", err)
	}

	if gotPath != "/chat.unfurl" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer xoxb-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	unfurls, ok := gotBody["unfurls"].(map[string]any)
	if !ok {
		t.Fatalf("unfurls missing or wrong type: %+v", gotBody)
	}
	// Keyed by the URL exactly as Slack reported it — a re-normalised key does
	// not match and the preview silently never appears.
	if _, ok := unfurls[link]; !ok {
		t.Fatalf("unfurls not keyed by the shared URL: %+v", unfurls)
	}
	if _, present := gotBody["user_auth_required"]; present {
		t.Fatalf("a successful preview must not also ask for auth: %+v", gotBody)
	}
}

func TestUnfurl_UserAuthPromptSendsAnEmptyMap(t *testing.T) {
	var gotBody map[string]any
	c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		io.WriteString(w, `{"ok":true}`)
	})

	if _, err := c.Unfurl(context.Background(), "xoxb-token", UnfurlRequest{
		Channel:          "C123",
		TS:               "1735689600.000100",
		UserAuthRequired: true,
		UserAuthURL:      "https://app.agora.dev/mobile/settings?tab=notifications",
	}); err != nil {
		t.Fatalf("Unfurl: %v", err)
	}
	if gotBody["user_auth_required"] != true {
		t.Fatalf("user_auth_required not sent: %+v", gotBody)
	}
	// Slack requires the field; `null` is rejected where `{}` is accepted.
	unfurls, ok := gotBody["unfurls"].(map[string]any)
	if !ok || len(unfurls) != 0 {
		t.Fatalf("unfurls = %v, want an empty object", gotBody["unfurls"])
	}
}

// Malformed / refused responses degrade into a typed error, never a panic and
// never a silent success.
func TestUnfurl_MalformedResponses(t *testing.T) {
	t.Run("ok false carries slack's own code", func(t *testing.T) {
		c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"ok":false,"error":"cannot_unfurl_url"}`)
		})
		_, err := c.Unfurl(context.Background(), "xoxb", UnfurlRequest{Channel: "C1", TS: "1"})
		if err == nil || !strings.Contains(err.Error(), "cannot_unfurl_url") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a dead token is recognised so the installation can be marked", func(t *testing.T) {
		c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"ok":false,"error":"token_revoked"}`)
		})
		_, err := c.Unfurl(context.Background(), "xoxb", UnfurlRequest{Channel: "C1", TS: "1"})
		if !IsTokenInvalid(err) {
			t.Fatalf("expected a token-invalid error, got %v", err)
		}
	})
	t.Run("a body that is not json is an error, not a zero value", func(t *testing.T) {
		c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<html>gateway</html>`)
		})
		if _, err := c.Unfurl(context.Background(), "xoxb", UnfurlRequest{Channel: "C1", TS: "1"}); err == nil {
			t.Fatal("expected a decode error")
		}
	})
	t.Run("no token never reaches the network", func(t *testing.T) {
		c := newTestAPIClient(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("must not call slack without a token")
		})
		if _, err := c.Unfurl(context.Background(), "", UnfurlRequest{Channel: "C1", TS: "1"}); err == nil {
			t.Fatal("expected not_authed")
		}
	})
}
