package handler

import "strings"

// The markdown-table subset the report pipeline reads.
//
// Reports are agent-authored markdown. Two consumers parse them: the
// spreadsheet renderer, which turns a table into real cells, and the Telegram
// delivery decision, which attaches a reply precisely when it contains one.
// Both must agree on what counts as a table — a reply detected as tabular but
// not rendered as one would arrive as a spreadsheet full of prose rows.

// splitTableRow splits a markdown table row into trimmed cells, dropping the
// empty fields the leading and trailing pipes produce.
func splitTableRow(line string) []string {
	trimmed := strings.Trim(strings.TrimSpace(line), "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, p := range parts {
		cells = append(cells, strings.TrimSpace(p))
	}
	return cells
}

// isTableDivider matches the |---|---| separator under a table header. The
// divider is what makes a line a table rather than prose that happens to
// contain a pipe.
func isTableDivider(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.HasPrefix(t, "|") || !strings.Contains(t, "-") {
		return false
	}
	for _, r := range t {
		if r != '|' && r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return true
}
