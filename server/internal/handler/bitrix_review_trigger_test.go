package handler

import "testing"

// TestBitrixCodeReviewEntered pins the ENTRY semantics that make the trigger
// safe against the poll loop. The poller re-reads every active task on a fixed
// interval, so "is in Code Review" must never be the condition — only the
// transition into it may fire a review.
func TestBitrixCodeReviewEntered(t *testing.T) {
	cases := []struct {
		name       string
		prev, next string
		want       bool
	}{
		{"dev column into code review", "Выполняются", "Code Review", true},
		{"testing into code review", "Ready for testing", "Code Review", true},
		{"first sync, unknown previous column", "", "Code Review", true},
		{"returned then back into review", "Returned", "In Code Review", true},
		{"parked in code review across polls", "Code Review", "Code Review", false},
		{"review column renamed between polls", "Review", "In Code Review", false},
		{"code review into need merge", "Code Review", "Need Merge", false},
		{"code review into testing", "Code Review", "Testing web", false},
		{"code review into returned", "Code Review", "Returned", false},
		{"unrelated move", "To Do", "Выполняются", false},
		{"stage dropped entirely", "Code Review", "", false},
		{"no stage at all", "", "", false},
	}
	for _, c := range cases {
		if got := bitrixCodeReviewEntered(c.prev, c.next); got != c.want {
			t.Errorf("%s: bitrixCodeReviewEntered(%q, %q) = %v, want %v", c.name, c.prev, c.next, got, c.want)
		}
	}
}

// TestBitrixStageFromMetadata covers the previous-column read, including the
// shapes a real issue row carries: no metadata at all, unrelated keys, and a
// whitespace-padded value.
func TestBitrixStageFromMetadata(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"stage present", `{"bitrix_stage":"Code Review"}`, "Code Review"},
		{"padded value", `{"bitrix_stage":"  Code Review  "}`, "Code Review"},
		{"other keys only", `{"bitrix_task_id":"12345"}`, ""},
		{"wrong type", `{"bitrix_stage":42}`, ""},
		{"empty object", `{}`, ""},
		{"nil metadata", ``, ""},
		{"malformed json", `{not json`, ""},
	}
	for _, c := range cases {
		var raw []byte
		if c.raw != "" {
			raw = []byte(c.raw)
		}
		if got := bitrixStageFromMetadata(raw); got != c.want {
			t.Errorf("%s: bitrixStageFromMetadata(%q) = %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}

// TestIssueQATargetOverride covers the per-issue QA target override: CI stamps a
// throwaway environment URL for this branch and the E2E pass must prefer it. A
// non-URL value is ignored rather than passed into an agent instruction.
func TestIssueQATargetOverride(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"https review app", `{"qa_target_url":"https://btx-4821.review.sdteam.uz"}`, "https://btx-4821.review.sdteam.uz"},
		{"http localhost", `{"qa_target_url":"http://127.0.0.1:8080"}`, "http://127.0.0.1:8080"},
		{"padded", `{"qa_target_url":"  https://x.test  "}`, "https://x.test"},
		{"not a url", `{"qa_target_url":"review.sdteam.uz"}`, ""},
		{"empty string", `{"qa_target_url":""}`, ""},
		{"absent", `{"pr_number":7}`, ""},
		{"nil metadata", ``, ""},
	}
	for _, c := range cases {
		var raw []byte
		if c.raw != "" {
			raw = []byte(c.raw)
		}
		if got := issueQATargetOverride(raw); got != c.want {
			t.Errorf("%s: issueQATargetOverride(%q) = %q, want %q", c.name, c.raw, got, c.want)
		}
	}
}
