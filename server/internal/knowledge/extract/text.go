package extract

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	mdATXHeading  = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	mdSetextLine  = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	mdThematic    = regexp.MustCompile(`^ {0,3}((?:\*[ \t]*){3,}|(?:-[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	mdListItem    = regexp.MustCompile(`^([ \t]*)([-*+]|\d{1,9}[.)])([ \t]+|$)`)
	mdFence       = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	mdDelimCell   = regexp.MustCompile(`^:?-+:?$`)
	mdInlineLink  = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	plainListItem = regexp.MustCompile(`^([-*+•●○◦▪▫■□‣⁃–]|\d{1,3}[.)]|[a-zA-Z]\))[ \t]+\S`)
	plainNumbered = regexp.MustCompile(`^(\d{1,2}(?:\.\d{1,2}){1,2}\.?|\d{1,2}\.)[ \t]+(\S.*)$`)
	layoutGap     = regexp.MustCompile(`\S {3,}\S`)
)

// ---- Markdown ----

func extractMarkdown(data []byte, b *builder) (Document, error) {
	src, err := decodeText(data)
	if err != nil {
		return Document{}, err
	}
	lines := strings.Split(sanitize(src), "\n")
	title := ""
	var para []string
	flush := func() error {
		if len(para) == 0 {
			return nil
		}
		text := strings.Join(para, "\n")
		para = nil
		return b.addText(KindParagraph, text, "")
	}
	heading := func(level int, text string) error {
		text = stripInline(text)
		if level == 1 && title == "" {
			title = text
		}
		return b.addHeading(level, text, "")
	}

	i := skipFrontMatter(lines)
	for i < len(lines) {
		if err := b.ctx.Err(); err != nil {
			return Document{}, err
		}
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			if err := flush(); err != nil {
				return Document{}, err
			}
			i++
		case mdFence.MatchString(line):
			if err := flush(); err != nil {
				return Document{}, err
			}
			var text string
			text, i = readFence(lines, i)
			if err := b.addText(KindParagraph, text, ""); err != nil {
				return Document{}, err
			}
		case mdATXHeading.MatchString(line):
			if err := flush(); err != nil {
				return Document{}, err
			}
			m := mdATXHeading.FindStringSubmatch(line)
			if err := heading(len(m[1]), m[2]); err != nil {
				return Document{}, err
			}
			i++
		case len(para) == 1 && mdSetextLine.MatchString(line):
			level := 2
			if strings.HasPrefix(trimmed, "=") {
				level = 1
			}
			text := para[0]
			para = nil
			if err := heading(level, text); err != nil {
				return Document{}, err
			}
			i++
		case mdThematic.MatchString(line):
			if err := flush(); err != nil {
				return Document{}, err
			}
			i++
		case i+1 < len(lines) && isPipeTableStart(line, lines[i+1]):
			if err := flush(); err != nil {
				return Document{}, err
			}
			var t *Table
			t, i = readPipeTable(lines, i)
			if err := b.add(Block{Kind: KindTable, Table: t}); err != nil {
				return Document{}, err
			}
		case mdListItem.MatchString(line) && (len(para) == 0 || !startsWithSpace(line)):
			if err := flush(); err != nil {
				return Document{}, err
			}
			var text string
			text, i = readMarkdownList(lines, i)
			if err := b.addText(KindList, text, ""); err != nil {
				return Document{}, err
			}
		default:
			para = append(para, trimmed)
			i++
		}
	}
	if err := flush(); err != nil {
		return Document{}, err
	}
	return Document{Title: title}, nil
}

// skipFrontMatter returns the index of the first line after a leading YAML
// front-matter block ("---" … "---"), or 0 when there is none.
func skipFrontMatter(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines) && i < 100; i++ {
		if t := strings.TrimSpace(lines[i]); t == "---" || t == "..." {
			return i + 1
		}
	}
	return 0
}

// readFence returns a fenced code block verbatim (fences included) and the
// index of the line after it.
func readFence(lines []string, i int) (string, int) {
	open := mdFence.FindStringSubmatch(lines[i])[1]
	out := []string{strings.TrimSpace(lines[i])}
	j := i + 1
	for ; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		out = append(out, strings.TrimRight(lines[j], " \t"))
		if strings.HasPrefix(t, open[:3]) && strings.Trim(t, open[:1]) == "" && len(t) >= len(open) {
			j++
			break
		}
	}
	return strings.Join(out, "\n"), j
}

func startsWithSpace(s string) bool {
	return s != "" && (s[0] == ' ' || s[0] == '\t')
}

func isBlockStart(line string) bool {
	return mdATXHeading.MatchString(line) || mdFence.MatchString(line) || mdThematic.MatchString(line)
}

// readMarkdownList collects a list (items, indented continuation lines,
// lazy continuation lines, blank lines between items) starting at i. Bullet
// markers are normalized to "-"; ordered markers are kept. A top-level item
// of the other kind (bullet vs ordered) starts a new list.
func readMarkdownList(lines []string, i int) (string, int) {
	var out []string
	first := mdListItem.FindStringSubmatch(lines[i])
	baseIndent, baseOrdered := expandIndent(first[1]), isOrderedMarker(first[2])
	j := i
	for j < len(lines) {
		line := lines[j]
		if strings.TrimSpace(line) == "" {
			// A blank line continues the list only if the next non-blank line
			// is another item or an indented continuation.
			k := j + 1
			for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
				k++
			}
			if k < len(lines) && (mdListItem.MatchString(lines[k]) || startsWithSpace(lines[k])) && !isBlockStart(lines[k]) {
				j = k
				continue
			}
			break
		}
		if j > i && isBlockStart(line) {
			break
		}
		if m := mdListItem.FindStringSubmatchIndex(line); m != nil {
			indent := expandIndent(line[m[2]:m[3]])
			marker := line[m[4]:m[5]]
			if j > i && indent <= baseIndent && isOrderedMarker(marker) != baseOrdered {
				break
			}
			if marker == "*" || marker == "+" {
				marker = "-"
			}
			rest := strings.TrimSpace(line[m[7]:])
			out = append(out, strings.Repeat(" ", indent)+marker+" "+rest)
		} else {
			out = append(out, strings.TrimRight(line, " \t"))
		}
		j++
	}
	return strings.Join(out, "\n"), j
}

func expandIndent(s string) int {
	n := 0
	for _, r := range s {
		if r == '\t' {
			n += 4
		} else {
			n++
		}
	}
	return n
}

func isPipeTableStart(header, delim string) bool {
	if !strings.Contains(header, "|") || !strings.Contains(delim, "|") || !strings.Contains(delim, "-") {
		return false
	}
	hc := splitPipeRow(header)
	dc := splitPipeRow(delim)
	if len(hc) != len(dc) {
		return false
	}
	for _, c := range dc {
		if !mdDelimCell.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

func readPipeTable(lines []string, i int) (*Table, int) {
	t := &Table{Header: splitPipeRow(lines[i])}
	j := i + 2
	for ; j < len(lines); j++ {
		line := lines[j]
		if strings.TrimSpace(line) == "" || !strings.Contains(line, "|") || isBlockStart(line) {
			break
		}
		t.Rows = append(t.Rows, splitPipeRow(line))
	}
	return t, j
}

// splitPipeRow splits a GFM table row into trimmed cells, honoring "\|".
func splitPipeRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	for k := 0; k < len(s); k++ {
		switch {
		case s[k] == '\\' && k+1 < len(s) && s[k+1] == '|':
			cur.WriteByte('|')
			k++
		case s[k] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(s[k])
		}
	}
	return append(cells, strings.TrimSpace(cur.String()))
}

// stripInline removes the most common inline markdown from heading text.
func stripInline(s string) string {
	s = mdInlineLink.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return strings.TrimSpace(s)
}

// ---- Plain text (also used for PDF pages) ----

func extractText(data []byte, b *builder) (Document, error) {
	src, err := decodeText(data)
	if err != nil {
		return Document{}, err
	}
	segs := segmentPlain(sanitize(src), "", false)
	for _, s := range markPlainHeadings(segs, false) {
		if err := b.ctx.Err(); err != nil {
			return Document{}, err
		}
		if err := s.add(b); err != nil {
			return Document{}, err
		}
	}
	return Document{}, nil
}

// plainSeg is one blank-line-separated segment of plain text.
type plainSeg struct {
	kind   string // KindParagraph | KindList | KindHeading
	level  int
	text   string
	single bool // the segment was one source line
	loc    string
}

func (s plainSeg) add(b *builder) error {
	if s.kind == KindHeading {
		return b.addHeading(s.level, s.text, s.loc)
	}
	return b.addText(s.kind, s.text, s.loc)
}

// segmentPlain splits text into paragraphs on blank lines, and turns runs of
// bullet/number-prefixed lines into lists. layout is true for pdftotext
// -layout output: prose lines get their justification spaces collapsed and
// line-end hyphenation joined, while column-aligned (tabular) paragraphs are
// kept as-is.
func segmentPlain(text, loc string, layout bool) []plainSeg {
	var segs []plainSeg
	var para []string
	flush := func() {
		if len(para) > 0 {
			segs = append(segs, splitPlainParagraph(dedent(para), loc, layout)...)
		}
		para = nil
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		para = append(para, strings.TrimRight(line, " \t"))
	}
	flush()
	return segs
}

func splitPlainParagraph(lines []string, loc string, layout bool) []plainSeg {
	if len(lines) == 1 && plainNumbered.MatchString(strings.TrimSpace(lines[0])) {
		// A lone numbered line ("1. Scope") may be a heading; markPlainHeadings decides.
		return []plainSeg{{kind: KindParagraph, text: joinPlainLines(lines, layout), single: true, loc: loc}}
	}
	first := -1
	for i, l := range lines {
		if plainListItem.MatchString(strings.TrimSpace(l)) {
			first = i
			break
		}
	}
	var segs []plainSeg
	textLines := lines
	if first >= 0 {
		textLines = lines[:first]
	}
	if len(textLines) > 0 {
		segs = append(segs, plainSeg{
			kind:   KindParagraph,
			text:   joinPlainLines(textLines, layout),
			single: len(textLines) == 1 && first < 0,
			loc:    loc,
		})
	}
	if first >= 0 {
		segs = append(segs, plainSeg{kind: KindList, text: plainList(lines[first:], layout), loc: loc})
	}
	return segs
}

// plainList renders list lines as markdown items; lines without a marker
// continue the previous item.
func plainList(lines []string, layout bool) string {
	var items []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if layout {
			t = collapseSpaces(t)
		}
		if m := plainListItem.FindStringSubmatch(t); m != nil {
			marker := m[1]
			rest := strings.TrimSpace(t[len(marker):])
			if !isOrderedMarker(marker) {
				marker = "-"
			}
			items = append(items, marker+" "+rest)
			continue
		}
		if len(items) == 0 {
			items = append(items, "- "+t)
			continue
		}
		items[len(items)-1] = joinHyphenated(items[len(items)-1], t)
	}
	return strings.Join(items, "\n")
}

func isOrderedMarker(m string) bool {
	if m == "" {
		return false
	}
	last := m[len(m)-1]
	return (last == '.' || last == ')') && len(m) > 1
}

func joinPlainLines(lines []string, layout bool) string {
	if !layout {
		return strings.Join(lines, "\n")
	}
	if isTabular(lines) {
		return strings.Join(lines, "\n")
	}
	out := ""
	for i, l := range lines {
		t := collapseSpaces(strings.TrimSpace(l))
		if i == 0 {
			out = t
			continue
		}
		out = joinHyphenated(out, t)
	}
	return out
}

// joinHyphenated joins two visual lines of one paragraph, removing a
// line-end hyphen when a lowercase word continues on the next line.
func joinHyphenated(a, b string) string {
	if strings.HasSuffix(a, "-") && len(a) > 1 {
		prev, _ := utf8.DecodeLastRuneInString(a[:len(a)-1])
		next, _ := utf8.DecodeRuneInString(b)
		if unicode.IsLetter(prev) && unicode.IsLower(next) {
			return a[:len(a)-1] + b
		}
	}
	return a + "\n" + b
}

// isTabular reports whether a pdftotext -layout paragraph looks like
// column-aligned data (most lines have wide gaps between words).
func isTabular(lines []string) bool {
	if len(lines) < 2 {
		return false
	}
	n := 0
	for _, l := range lines {
		if layoutGap.MatchString(strings.TrimSpace(l)) {
			n++
		}
	}
	return n*2 >= len(lines)
}

func dedent(lines []string) []string {
	minIndent := -1
	for _, l := range lines {
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l[minIndent:]
	}
	return out
}

// markPlainHeadings promotes single-line paragraphs to headings when they are
// clearly headings: short, no sentence punctuation at the end, numbered
// ("2.1 Approval") or ALL CAPS — or, when titleCase is set (PDFs), a short
// capitalized line — and followed by body text. A heading may also be
// followed by another numbered/ALL CAPS heading that is itself followed by
// body text.
func markPlainHeadings(segs []plainSeg, titleCase bool) []plainSeg {
	type cand struct {
		level  int
		text   string
		strong bool
	}
	cands := make([]*cand, len(segs))
	for i, s := range segs {
		if s.kind != KindParagraph || !s.single {
			continue
		}
		if level, text, strong, ok := plainHeadingCandidate(s.text, titleCase); ok {
			cands[i] = &cand{level: level, text: text, strong: strong}
		}
	}
	isHeading := make([]bool, len(segs))
	for i := len(segs) - 1; i >= 0; i-- {
		c := cands[i]
		if c == nil || i+1 >= len(segs) {
			continue
		}
		next := segs[i+1]
		switch {
		case cands[i+1] == nil && next.kind != KindHeading:
			// Followed by body text. Title-case candidates need a real
			// paragraph after them, not another short line.
			isHeading[i] = c.strong || utf8.RuneCountInString(next.text) >= 2*utf8.RuneCountInString(c.text)
		case isHeading[i+1] && c.strong && cands[i+1].strong:
			isHeading[i] = true
		}
	}
	out := make([]plainSeg, len(segs))
	for i, s := range segs {
		out[i] = s
		if isHeading[i] {
			out[i] = plainSeg{kind: KindHeading, level: cands[i].level, text: cands[i].text, loc: s.loc}
		}
	}
	return out
}

func plainHeadingCandidate(line string, titleCase bool) (level int, text string, strong, ok bool) {
	t := strings.TrimSpace(line)
	n := utf8.RuneCountInString(t)
	if n == 0 || n > 80 || strings.ContainsRune(t, '\n') {
		return 0, "", false, false
	}
	last, _ := utf8.DecodeLastRuneInString(t)
	if strings.ContainsRune(".,;:!?", last) {
		return 0, "", false, false
	}
	if !hasLetter(t) {
		return 0, "", false, false
	}
	if m := plainNumbered.FindStringSubmatch(t); m != nil {
		first, _ := utf8.DecodeRuneInString(m[2])
		if unicode.IsUpper(first) {
			return strings.Count(strings.TrimSuffix(m[1], "."), ".") + 1, t, true, true
		}
		return 0, "", false, false
	}
	if isAllCaps(t) {
		return 1, t, true, true
	}
	if titleCase && n <= 60 && len(strings.Fields(t)) <= 8 {
		first, _ := utf8.DecodeRuneInString(t)
		if unicode.IsUpper(first) {
			return 2, t, false, true
		}
	}
	return 0, "", false, false
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// isAllCaps reports whether s has at least three cased letters and all of them
// are uppercase.
func isAllCaps(s string) bool {
	upper := 0
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsUpper(r) {
			upper++
		}
	}
	return upper >= 3
}
