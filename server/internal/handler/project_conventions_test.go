package handler

import (
	"strings"
	"testing"
)

// renderConventionsContext must label the block, demand compliance, and carry
// the text verbatim — this is the string an agent reads on every claim.
func TestRenderConventionsContext(t *testing.T) {
	out := renderConventionsContext("- Use hooks\n- Tailwind tokens only", "PROJECT CONVENTIONS")
	for _, want := range []string{"PROJECT CONVENTIONS", "MUST follow", "Use hooks", "Tailwind tokens only"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered context missing %q\ngot: %s", want, out)
		}
	}
}

// Over-budget conventions are truncated, not injected whole.
func TestRenderConventionsContext_Truncates(t *testing.T) {
	big := strings.Repeat("x", conventionsMaxChars+500)
	out := renderConventionsContext(big, "PROJECT CONVENTIONS")
	if !strings.Contains(out, "…(truncated)") {
		t.Error("expected truncation marker for over-budget conventions")
	}
	if len(out) > conventionsMaxChars+200 {
		t.Errorf("truncated output still too long: %d", len(out))
	}
}

// conventionsFromSettings pulls the trimmed value, and rejects empty/absent.
func TestConventionsFromSettings(t *testing.T) {
	if got, ok := conventionsFromSettings([]byte(`{"conventions":"  rule one  "}`)); !ok || got != "rule one" {
		t.Errorf("want (rule one,true), got (%q,%v)", got, ok)
	}
	if _, ok := conventionsFromSettings([]byte(`{"conventions":"   "}`)); ok {
		t.Error("whitespace-only conventions should be treated as unset")
	}
	if _, ok := conventionsFromSettings([]byte(`{"sprint_mode":true}`)); ok {
		t.Error("absent conventions key should be unset")
	}
}
