package telegram

import (
	"strings"
	"testing"
)

func TestMarkdownBoldAndItalic(t *testing.T) {
	// The visible symptom this fixes: readers saw literal ** around the number
	// that mattered.
	if got := MarkdownToHTML("**58 tadan 8 tasi** yopilgan"); got != "<b>58 tadan 8 tasi</b> yopilgan" {
		t.Errorf("bold: got %q", got)
	}
	if got := MarkdownToHTML("bu _muhim_ narsa"); got != "bu <i>muhim</i> narsa" {
		t.Errorf("italic: got %q", got)
	}
}

func TestMarkdownLeavesArithmeticAlone(t *testing.T) {
	// A lone asterisk between spaces is multiplication, not emphasis. Treating
	// it as a marker silently eats the operator.
	for _, in := range []string{"2 * 3 * 4", "a_b_c", "50 * 2 = 100"} {
		if got := MarkdownToHTML(in); strings.Contains(got, "<i>") {
			t.Errorf("%q was italicised: %q", in, got)
		}
	}
}

func TestMarkdownEscapesBeforeAddingTags(t *testing.T) {
	// Agent text is untrusted. Escaping after conversion would strip the tags
	// just produced; escaping before is what keeps both correct.
	got := MarkdownToHTML("**a < b & c** <script>x</script>")
	if !strings.Contains(got, "<b>a &lt; b &amp; c</b>") {
		t.Errorf("content not escaped inside bold: %q", got)
	}
	if strings.Contains(got, "<script>") {
		t.Errorf("markup survived: %q", got)
	}
}

func TestMarkdownCodeIsLiteral(t *testing.T) {
	// Emphasis markers inside code are content. Converting them would rewrite
	// the very string the agent is quoting.
	got := MarkdownToHTML("ishlat `GET /api?tag=**bug**`")
	if !strings.Contains(got, "<code>GET /api?tag=**bug**</code>") {
		t.Errorf("code span was reinterpreted: %q", got)
	}
	block := MarkdownToHTML("```\nline **one**\n```")
	if !strings.Contains(block, "<pre>line **one**\n</pre>") {
		t.Errorf("code block was reinterpreted: %q", block)
	}
}

func TestMarkdownDegradesUnsupportedConstructs(t *testing.T) {
	// Telegram has no heading, list or rule. Dropping them would lose the
	// report's structure; leaving them raw shows the reader markup.
	if got := MarkdownToHTML("## Asosiy raqamlar"); got != "<b>Asosiy raqamlar</b>" {
		t.Errorf("heading: got %q", got)
	}
	if got := MarkdownToHTML("- birinchi\n- ikkinchi"); got != "• birinchi\n• ikkinchi" {
		t.Errorf("bullets: got %q", got)
	}
	if got := MarkdownToHTML("---"); got != "———" {
		t.Errorf("rule: got %q", got)
	}
}

func TestMarkdownLinks(t *testing.T) {
	got := MarkdownToHTML("[hisobot](https://example.com/a)")
	if got != `<a href="https://example.com/a">hisobot</a>` {
		t.Errorf("got %q", got)
	}
}

func TestMarkdownLeavesTablesReadable(t *testing.T) {
	// A table has no Telegram form. It must survive as text rather than
	// becoming broken markup — the delivery layer decides tables belong in a
	// document instead.
	got := MarkdownToHTML("| Oy | Soni |\n|---|---|\n| Yanvar | 360 |")
	if !strings.Contains(got, "| Yanvar | 360 |") {
		t.Errorf("table row mangled: %q", got)
	}
}
