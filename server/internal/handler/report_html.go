package handler

import (
	"fmt"
	"html"
	"strings"
)

// Rendering an agent's markdown report into a standalone HTML page.
//
// Telegram cannot display a markdown table — the weekly report's whole point is
// a per-assignee table, and pasted as message text it collapses into unreadable
// pipe soup on a phone. So the report goes out as an attached HTML document
// instead, with a short caption carrying the headline.
//
// This is NOT a general markdown implementation and must not grow into one. It
// covers exactly the constructs these reports use — headings, bold, inline
// code, tables, bullets, rules, paragraphs — and passes anything else through
// as escaped text. A partial renderer that is honest about its scope beats a
// half-correct general one: unsupported syntax shows up as literal characters,
// which is visible and fixable, rather than silently mangling a number.
//
// Everything dynamic is HTML-escaped before it reaches the template. The input
// is agent-authored text, so it is untrusted by construction.

// inlineMarkdown renders the inline subset: **bold**, `code`. Escaping happens
// FIRST so agent text can never inject markup; the tags below are added after.
func inlineMarkdown(s string) string {
	out := html.EscapeString(s)

	// `code` — done before bold so a backtick span containing ** is left alone.
	for {
		start := strings.Index(out, "`")
		if start < 0 {
			break
		}
		end := strings.Index(out[start+1:], "`")
		if end < 0 {
			break
		}
		end += start + 1
		out = out[:start] + "<code>" + out[start+1:end] + "</code>" + out[end+1:]
	}

	// **bold** — paired; an unmatched ** stays literal rather than opening a
	// tag that swallows the rest of the report.
	for {
		start := strings.Index(out, "**")
		if start < 0 {
			break
		}
		end := strings.Index(out[start+2:], "**")
		if end < 0 {
			break
		}
		end += start + 2
		out = out[:start] + "<strong>" + out[start+2:end] + "</strong>" + out[end+2:]
	}
	return out
}

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

// isTableDivider matches the |---|---| separator under a table header.
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

// renderReportBody converts the report's markdown body to an HTML fragment.
func renderReportBody(md string) string {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var b strings.Builder
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		fmt.Fprintf(&b, "<p>%s</p>\n", inlineMarkdown(strings.Join(para, " ")))
		para = nil
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		switch {
		case trimmed == "":
			flushPara()

		case strings.HasPrefix(trimmed, "---") && strings.Trim(trimmed, "-") == "":
			flushPara()
			b.WriteString("<hr>\n")

		case strings.HasPrefix(trimmed, "#"):
			flushPara()
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level > 6 {
				level = 6
			}
			// The report's own H1/H2 sit inside a page that already has a
			// title, so everything shifts down one level — an H1 mid-document
			// would out-shout the page heading.
			tag := fmt.Sprintf("h%d", min(level+1, 6))
			fmt.Fprintf(&b, "<%s>%s</%s>\n", tag, inlineMarkdown(strings.TrimSpace(trimmed[level:])), tag)

		case strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableDivider(lines[i+1]):
			flushPara()
			header := splitTableRow(trimmed)
			b.WriteString("<div class=\"tw\"><table>\n<thead><tr>")
			for _, c := range header {
				fmt.Fprintf(&b, "<th>%s</th>", inlineMarkdown(c))
			}
			b.WriteString("</tr></thead>\n<tbody>\n")
			i += 2 // skip the divider
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				b.WriteString("<tr>")
				for _, c := range splitTableRow(lines[i]) {
					fmt.Fprintf(&b, "<td>%s</td>", inlineMarkdown(c))
				}
				b.WriteString("</tr>\n")
				i++
			}
			i-- // the outer loop advances
			b.WriteString("</tbody>\n</table></div>\n")

		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			flushPara()
			b.WriteString("<ul>\n")
			for i < len(lines) {
				t := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ") {
					break
				}
				fmt.Fprintf(&b, "<li>%s</li>\n", inlineMarkdown(strings.TrimSpace(t[2:])))
				i++
			}
			i--
			b.WriteString("</ul>\n")

		default:
			para = append(para, trimmed)
		}
	}
	flushPara()
	return b.String()
}

// renderReportHTML wraps the rendered body in a standalone, self-contained
// page. No external stylesheet or font: the file is opened straight from a
// Telegram attachment, often offline, so anything remote would simply not load.
func renderReportHTML(title, md string) []byte {
	page := `<!doctype html>
<html lang="uz">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(title) + `</title>
<style>
  :root {
    --ground:#f6f7f9; --panel:#fff; --line:#dde1e7; --line-soft:#eaedf1;
    --ink:#171c23; --ink-2:#4a5462; --ink-3:#78828f;
    --warn:#b8763a; --good:#2c6f68; --alert:#a8433a;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --ground:#10141a; --panel:#161b23; --line:#2a323d; --line-soft:#222932;
      --ink:#e8ecf2; --ink-2:#a3adbb; --ink-3:#78828f;
      --warn:#d59a5f; --good:#5aa79e; --alert:#d4695e;
    }
  }
  *{box-sizing:border-box}
  body{
    margin:0;background:var(--ground);color:var(--ink);
    font-family:system-ui,-apple-system,"Segoe UI",sans-serif;
    font-size:15px;line-height:1.6;-webkit-font-smoothing:antialiased;
  }
  .wrap{max-width:760px;margin:0 auto;padding:28px 18px 56px}
  .masthead{border-bottom:2px solid var(--ink);padding-bottom:12px;margin-bottom:22px}
  .eyebrow{
    font-size:11px;letter-spacing:.11em;text-transform:uppercase;
    color:var(--ink-3);font-weight:600;margin-bottom:6px
  }
  h1{
    margin:0;font-size:23px;line-height:1.2;font-weight:600;
    letter-spacing:-.01em;text-wrap:balance;
    font-family:"Iowan Old Style","Palatino Linotype",Palatino,Georgia,serif;
  }
  h2{
    margin:26px 0 10px;font-size:12px;font-weight:700;
    letter-spacing:.09em;text-transform:uppercase;color:var(--ink-2);
  }
  h3,h4,h5,h6{margin:20px 0 8px;font-size:15px;font-weight:600}
  p{margin:0 0 12px}
  strong{font-weight:650;color:var(--ink)}
  code{
    font-family:ui-monospace,"SF Mono",Menlo,Consolas,monospace;
    font-size:12.5px;background:var(--line-soft);
    padding:1px 5px;border-radius:2px;
  }
  hr{border:0;border-top:1px solid var(--line);margin:22px 0}
  ul{margin:0 0 12px;padding-left:20px}
  li{margin-bottom:5px}
  .tw{overflow-x:auto;margin:0 0 16px}
  table{border-collapse:collapse;width:100%;font-size:14px}
  th{
    text-align:left;font-size:11px;letter-spacing:.06em;text-transform:uppercase;
    color:var(--ink-3);font-weight:600;
    padding:0 10px 7px 0;border-bottom:1px solid var(--line);
  }
  td{
    padding:8px 10px 8px 0;border-bottom:1px solid var(--line-soft);
    font-variant-numeric:tabular-nums;vertical-align:top;
  }
  tr td:first-child{color:var(--ink)}
  tr td:not(:first-child){color:var(--ink-2)}
  .foot{margin-top:30px;padding-top:12px;border-top:1px solid var(--line);
        font-size:12px;color:var(--ink-3)}
</style>
</head>
<body>
<div class="wrap">
  <div class="masthead">
    <div class="eyebrow">Agora · Bitrix24</div>
    <h1>` + html.EscapeString(title) + `</h1>
  </div>
` + renderReportBody(md) + `  <div class="foot">Agora avtopiloti tomonidan tayyorlangan.</div>
</div>
</body>
</html>
`
	return []byte(page)
}
