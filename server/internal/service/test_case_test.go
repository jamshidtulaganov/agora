package service

import "testing"

func TestParseTestCasesBlock(t *testing.T) {
	t.Run("valid block", func(t *testing.T) {
		content := "Here are the cases.\n\n```test-cases\n" +
			`[{"title":"Login renders","steps":"1. open /login","expected":"shows Войти","kind":"automated"},` +
			`{"title":"Empty password blocked","steps":"submit empty","expected":"validation error","kind":"manual"}]` +
			"\n```\n"
		cases, ok := parseTestCasesBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if len(cases) != 2 {
			t.Fatalf("got %d cases, want 2", len(cases))
		}
		if cases[0].Title != "Login renders" || cases[0].Kind != "automated" {
			t.Errorf("case 0 = %+v", cases[0])
		}
	})

	t.Run("no block", func(t *testing.T) {
		if _, ok := parseTestCasesBlock("just a comment"); ok {
			t.Error("expected ok=false")
		}
	})

	t.Run("malformed json", func(t *testing.T) {
		if _, ok := parseTestCasesBlock("```test-cases\n[not json}\n```"); ok {
			t.Error("expected ok=false on malformed JSON")
		}
	})

	t.Run("empty array", func(t *testing.T) {
		cases, ok := parseTestCasesBlock("```test-cases\n[]\n```")
		if !ok || len(cases) != 0 {
			t.Errorf("ok=%v len=%d, want ok=true len=0", ok, len(cases))
		}
	})
}
