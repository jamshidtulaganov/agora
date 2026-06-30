package service

import "testing"

func TestParseQAResultBlock(t *testing.T) {
	t.Run("valid block amid prose", func(t *testing.T) {
		content := "## QA verdict\n\nAll new tests pass; 1 pre-existing failure ignored.\n\n" +
			"```qa-result\n" +
			`{"verdict":"fail","summary":"1 new failure","commands":[{"cmd":"go test ./...","baseline_exit":0,"branch_exit":1,"kind":"new_failure"}],"screenshots":["/var/www/x.png"]}` +
			"\n```\n"
		raw, p, ok := parseQAResultBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if p.Verdict != "fail" {
			t.Errorf("verdict = %q, want fail", p.Verdict)
		}
		if p.Summary != "1 new failure" {
			t.Errorf("summary = %q", p.Summary)
		}
		if len(p.Commands) != 1 {
			t.Errorf("commands len = %d, want 1", len(p.Commands))
		}
		if raw == "" {
			t.Error("raw should be the verbatim JSON")
		}
	})

	t.Run("no block", func(t *testing.T) {
		if _, _, ok := parseQAResultBlock("just a normal comment, no fenced block"); ok {
			t.Error("expected ok=false when no qa-result block")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		content := "```qa-result\n{not valid json}\n```"
		if _, _, ok := parseQAResultBlock(content); ok {
			t.Error("expected ok=false on malformed JSON")
		}
	})

	t.Run("invalid verdict downgrades to skip", func(t *testing.T) {
		content := "```qa-result\n" + `{"verdict":"maybe","summary":"x"}` + "\n```"
		if _, _, ok := parseQAResultBlock(content); ok {
			t.Error("expected ok=false on a verdict that is neither pass nor fail")
		}
	})

	t.Run("pass verdict", func(t *testing.T) {
		content := "```qa-result\n" + `{"verdict":"pass","summary":"all green","commands":[],"screenshots":[]}` + "\n```"
		_, p, ok := parseQAResultBlock(content)
		if !ok || p.Verdict != "pass" {
			t.Errorf("ok=%v verdict=%q, want ok=true pass", ok, p.Verdict)
		}
	})
}
