package handler

import (
	"context"
	"testing"
)

func TestBitrixTagToLabelName(t *testing.T) {
	cases := map[string]string{
		// Type-class tags are left for AI triage — not mirrored as labels.
		"bug":              "",
		"баг":              "",
		"#BugReport":       "",
		"feature":          "",
		"фича":             "",
		"новый функционал": "",
		"server":           "server",
		"DevOps":           "server",
		"urgent":           "urgent",
		"  ":               "",
		"#":                "",
	}
	for in, want := range cases {
		if got := bitrixTagToLabelName(in); got != want {
			t.Errorf("bitrixTagToLabelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBitrixTagsToLabelNamesDedupesAliases(t *testing.T) {
	got := bitrixTagsToLabelNames([]string{"баг", "bug", "BugReport", "urgent", "urgent", "server"})
	want := map[string]bool{"urgent": true, "server": true}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for _, n := range got {
		if !want[n] {
			t.Errorf("unexpected label %q in %v", n, got)
		}
	}
}

func TestBitrixWebhookAttachesTagLabels(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	portal := newBitrixMockPortal(t)
	configureBitrixEnv(t, portal.srv.URL)

	const taskID = "bx-tags-1"
	cleanupBitrixIssues(t, taskID)

	portal.setTask(taskID, `{
		"id":"`+taskID+`","title":"Tagged bug","status":3,
		"tags":["баг","urgent","ai"]
	}`)
	if w := postBitrixWebhook(t, "ONTASKADD", taskID); w.Code != 200 {
		t.Fatalf("webhook status = %d", w.Code)
	}
	id, _, _, _, count := issueByBitrixTaskID(t, taskID)
	if count != 1 || id == "" {
		t.Fatalf("issue count=%d id=%q", count, id)
	}

	rows, err := testPool.Query(context.Background(),
		`SELECT l.name FROM issue_label l
		   JOIN issue_to_label il ON il.label_id = l.id
		  WHERE il.issue_id = $1::uuid
		  ORDER BY l.name`, id)
	if err != nil {
		t.Fatalf("query labels: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got[name] = true
	}
	for _, want := range []string{"urgent", "ai"} {
		if !got[want] {
			t.Errorf("missing label %q; have %v", want, got)
		}
	}
	if got["type:bug"] || got["bug"] || got["баг"] {
		t.Errorf("type-class Bitrix tags must not become labels (AI triage owns type:*); have %v", got)
	}
}
