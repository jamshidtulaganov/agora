// Package chunk splits an extracted document into retrieval chunks: roughly
// page-sized markdown sections cut at headings, each carrying its heading
// breadcrumb and source location for citations. Tables become their own
// chunks with the header row repeated in every one.
package chunk

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jamshidtulaganov/agora/server/internal/knowledge/extract"
)

// Defaults for zero Options fields.
const (
	DefaultTargetChars       = 3000
	DefaultMaxChars          = 6000
	DefaultTableRowsPerChunk = 40
)

// PathSeparator joins HeadingPath elements.
const PathSeparator = " › "

// Chunk is one retrievable section of a document.
type Chunk struct {
	Ord         int    // 0-based, reading order
	HeadingPath string // "Collections SOP › Write-offs › Approval" (title first)
	Location    string // "p. 4" | "pp. 3–4" | "Sheet \"Rates\", rows 2–41" | ""
	Body        string // markdown (tables as pipe tables)
}

// Options tunes chunk sizes, in characters (runes).
type Options struct {
	TargetChars       int // consecutive paragraphs/lists merge up to this size
	MaxChars          int // a single paragraph longer than this is split
	TableRowsPerChunk int // data rows per table chunk
}

func (o Options) withDefaults() Options {
	if o.TargetChars <= 0 {
		o.TargetChars = DefaultTargetChars
	}
	if o.MaxChars <= 0 {
		o.MaxChars = DefaultMaxChars
	}
	if o.MaxChars < o.TargetChars {
		o.MaxChars = o.TargetChars
	}
	if o.TableRowsPerChunk <= 0 {
		o.TableRowsPerChunk = DefaultTableRowsPerChunk
	}
	return o
}

// Split turns doc into chunks. A heading of level 1-3 always starts a new
// chunk; its text goes into HeadingPath rather than the body (a heading with
// no content under it at all becomes a chunk of its own). Deeper headings
// stay in the body as markdown headings. Paragraphs and lists merge up to
// TargetChars; one longer than MaxChars is split on sentence boundaries (list
// items for lists), else hard-split. Output is deterministic.
func Split(doc extract.Document, opts Options) []Chunk {
	s := &splitter{opts: opts.withDefaults(), title: strings.TrimSpace(doc.Title)}
	for _, b := range doc.Blocks {
		s.block(b)
	}
	s.flush()
	s.closeSections(1)
	return s.out
}

type section struct {
	level    int
	text     string
	loc      string
	produced bool // a chunk was emitted while this heading was open
}

type part struct {
	text    string
	loc     string
	heading bool // a level 4-6 heading line
}

type splitter struct {
	opts  Options
	title string
	stack []*section
	path  string // HeadingPath of the pending chunk
	parts []part
	size  int // runes of the pending body, separators included
	out   []Chunk
}

func (s *splitter) block(b extract.Block) {
	switch b.Kind {
	case extract.KindHeading:
		text := strings.TrimSpace(strings.Join(strings.Fields(b.Text), " "))
		if text == "" {
			return
		}
		level := min(max(b.Level, 1), 6)
		if level <= 3 {
			s.flush()
		}
		s.closeSections(level)
		s.stack = append(s.stack, &section{level: level, text: text, loc: b.Location})
		if level > 3 && len(s.parts) > 0 {
			line := strings.Repeat("#", level) + " " + text
			if s.size+2+runeLen(line) > s.opts.TargetChars {
				// Start the next chunk here; the heading goes into its path.
				s.flush()
				return
			}
			s.append(part{text: line, loc: b.Location, heading: true})
			s.stack[len(s.stack)-1].produced = true
		}
	case extract.KindTable:
		s.flush()
		if b.Table != nil {
			s.table(b)
		}
	default:
		text := strings.TrimSpace(b.Text)
		if text == "" {
			return
		}
		// A leading paragraph that only repeats the document's title (Word
		// exports often carry it as a plain, bold first line) would become a
		// section with nothing in it.
		if len(s.out) == 0 && len(s.parts) == 0 && strings.EqualFold(text, s.title) {
			return
		}
		levels := []splitFunc{splitSentences, splitLines, splitWords}
		if b.Kind == extract.KindList {
			levels = []splitFunc{splitListItems, splitSentences, splitWords}
		}
		for _, piece := range splitLong(text, levels, s.opts.TargetChars, s.opts.MaxChars) {
			s.addText(piece, b.Location)
		}
	}
}

// addText appends body text, first flushing the pending chunk when the text
// would push it past TargetChars. A trailing level 4-6 heading moves to the
// next chunk (via its path) instead of ending this one.
func (s *splitter) addText(text, loc string) {
	n := runeLen(text)
	if len(s.parts) > 0 && s.size+2+n > s.opts.TargetChars {
		if last := s.parts[len(s.parts)-1]; last.heading && len(s.parts) > 1 {
			s.parts = s.parts[:len(s.parts)-1]
			s.size -= 2 + runeLen(last.text)
		}
		s.flush()
	}
	s.append(part{text: text, loc: loc})
}

func (s *splitter) append(p part) {
	if len(s.parts) == 0 {
		s.path = s.headingPath()
		s.size = 0
	} else {
		s.size += 2
	}
	s.parts = append(s.parts, p)
	s.size += runeLen(p.text)
}

// flush emits the pending body chunk, if any.
func (s *splitter) flush() {
	if len(s.parts) == 0 {
		return
	}
	texts := make([]string, len(s.parts))
	locs := make([]string, len(s.parts))
	for i, p := range s.parts {
		texts[i] = p.text
		locs[i] = p.loc
	}
	s.parts = nil
	s.size = 0
	s.emit(s.path, formatLocation(locs), strings.Join(texts, "\n\n"))
}

func (s *splitter) emit(path, loc, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	s.out = append(s.out, Chunk{Ord: len(s.out), HeadingPath: path, Location: loc, Body: body})
	for _, sec := range s.stack {
		sec.produced = true
	}
}

// closeSections pops headings of level >= level. A popped heading that never
// had content (nor sub-headings with content) becomes a chunk of its own so
// its text is not lost — unless it is the document title, which every
// chunk's path already starts with.
func (s *splitter) closeSections(level int) {
	if len(s.parts) > 0 {
		// Every open heading is in the pending chunk's path or body.
		for _, sec := range s.stack {
			sec.produced = true
		}
	}
	for len(s.stack) > 0 {
		top := s.stack[len(s.stack)-1]
		if top.level < level {
			return
		}
		if !top.produced && !(len(s.stack) == 1 && s.isTitle(top.text)) {
			s.emit(s.headingPath(), top.loc, strings.Repeat("#", top.level)+" "+top.text)
		}
		s.stack = s.stack[:len(s.stack)-1]
	}
}

func (s *splitter) isTitle(text string) bool {
	return s.title != "" && strings.EqualFold(text, s.title)
}

// headingPath is the title followed by the open headings. A first heading
// equal to the title is not repeated.
func (s *splitter) headingPath() string {
	parts := make([]string, 0, len(s.stack)+1)
	if s.title != "" {
		parts = append(parts, s.title)
	}
	for i, sec := range s.stack {
		if i == 0 && s.isTitle(sec.text) {
			continue
		}
		parts = append(parts, sec.text)
	}
	return strings.Join(parts, PathSeparator)
}

// table emits a table as chunks of up to TableRowsPerChunk non-empty rows
// (fewer when the rows would exceed MaxChars), each with the header row.
func (s *splitter) table(b extract.Block) {
	t := b.Table
	width := len(t.Header)
	for _, r := range t.Rows {
		width = max(width, len(r))
	}
	if width == 0 {
		return
	}
	head := renderRow(t.Header, width) + "\n" + strings.TrimSuffix(strings.Repeat("| --- ", width), " ") + " |"
	headLen := runeLen(head)
	path := s.headingPath()

	var rows []string
	first, last, size := -1, -1, headLen
	flush := func() {
		if len(rows) == 0 {
			return
		}
		s.emit(path, tableLocation(t, b.Location, first, last), head+"\n"+strings.Join(rows, "\n"))
		rows, first, last, size = nil, -1, -1, headLen
	}
	for i, r := range t.Rows {
		if isEmptyRow(r) {
			continue
		}
		line := renderRow(r, width)
		n := runeLen(line) + 1
		if len(rows) > 0 && (len(rows) >= s.opts.TableRowsPerChunk || size+n > s.opts.MaxChars) {
			flush()
		}
		if first < 0 {
			first = i
		}
		rows = append(rows, line)
		last = i
		size += n
	}
	if first < 0 && !isEmptyRow(t.Header) {
		// Header-only table.
		s.emit(path, tableLocation(t, b.Location, -1, -1), head)
		return
	}
	flush()
}

func tableLocation(t *extract.Table, blockLoc string, first, last int) string {
	rows := ""
	if t.FirstRow > 0 && first >= 0 {
		a, z := t.FirstRow+first, t.FirstRow+last
		if a == z {
			rows = "row " + strconv.Itoa(a)
		} else {
			rows = "rows " + strconv.Itoa(a) + "–" + strconv.Itoa(z)
		}
	}
	sheet := ""
	if t.Sheet != "" {
		sheet = `Sheet "` + t.Sheet + `"`
	}
	switch {
	case sheet != "" && rows != "":
		return sheet + ", " + rows
	case sheet != "":
		return sheet
	case rows != "":
		return rows
	}
	return formatLocation([]string{blockLoc})
}

func renderRow(cells []string, width int) string {
	var sb strings.Builder
	sb.WriteString("|")
	for i := 0; i < width; i++ {
		c := ""
		if i < len(cells) {
			c = cells[i]
		}
		c = strings.ReplaceAll(strings.TrimSpace(c), "|", `\|`)
		c = strings.ReplaceAll(c, "\n", "<br>")
		sb.WriteString(" ")
		sb.WriteString(c)
		sb.WriteString(" |")
	}
	return sb.String()
}

func isEmptyRow(r []string) bool {
	for _, c := range r {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// formatLocation merges block locations: PDF pages become "p. N" or
// "pp. A–B"; anything else is the first non-empty location.
func formatLocation(locs []string) string {
	lo, hi, other := 0, 0, ""
	for _, l := range locs {
		if l == "" {
			continue
		}
		if n, ok := pageNumber(l); ok {
			if lo == 0 || n < lo {
				lo = n
			}
			hi = max(hi, n)
			continue
		}
		if other == "" {
			other = l
		}
	}
	switch {
	case lo > 0 && lo == hi:
		return "p. " + strconv.Itoa(lo)
	case lo > 0:
		return "pp. " + strconv.Itoa(lo) + "–" + strconv.Itoa(hi)
	}
	return other
}

func pageNumber(loc string) (int, bool) {
	rest, ok := strings.CutPrefix(loc, "p. ")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n > 0
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// ---- long text splitting ----

// splitFunc cuts text into consecutive units whose concatenation is text.
type splitFunc func(string) []string

// splitLong returns text unchanged when it fits in max runes; otherwise it
// cuts it with the first split level that yields several units, packs units
// greedily up to target, and recurses into units still over max with the
// next level. The last resort is a hard cut.
func splitLong(text string, levels []splitFunc, target, max int) []string {
	if runeLen(text) <= max {
		return []string{text}
	}
	for i, split := range levels {
		units := split(text)
		if len(units) < 2 {
			continue
		}
		var out []string
		var cur strings.Builder
		curLen := 0
		push := func() {
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, t)
			}
			cur.Reset()
			curLen = 0
		}
		for _, u := range units {
			n := runeLen(u)
			if n > max {
				push()
				out = append(out, splitLong(u, levels[i+1:], target, max)...)
				continue
			}
			if curLen > 0 && curLen+n > target {
				push()
			}
			cur.WriteString(u)
			curLen += n
		}
		push()
		return out
	}
	return hardSplit(text, max)
}

// splitSentences cuts after sentence-ending punctuation (plus closing quotes
// or brackets) followed by whitespace.
func splitSentences(text string) []string {
	var units []string
	runes := []rune(text)
	start := 0
	for i := 0; i < len(runes); i++ {
		if !strings.ContainsRune(".!?…。！？", runes[i]) {
			continue
		}
		j := i + 1
		for j < len(runes) && strings.ContainsRune(`"')]»”’`, runes[j]) {
			j++
		}
		if j >= len(runes) || !unicode.IsSpace(runes[j]) {
			continue
		}
		for j < len(runes) && unicode.IsSpace(runes[j]) {
			j++
		}
		units = append(units, string(runes[start:j]))
		start = j
		i = j - 1
	}
	if start < len(runes) {
		units = append(units, string(runes[start:]))
	}
	return units
}

// splitLines cuts after each newline.
func splitLines(text string) []string {
	return strings.SplitAfter(text, "\n")
}

// splitListItems cuts before each top-level list item (a line that does not
// start with whitespace), keeping nested items and continuation lines with
// their parent.
func splitListItems(text string) []string {
	var units []string
	var cur strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		if cur.Len() > 0 && line != "" && line[0] != ' ' && line[0] != '\t' {
			units = append(units, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		units = append(units, cur.String())
	}
	return units
}

// splitWords cuts after each run of whitespace.
func splitWords(text string) []string {
	var units []string
	start := 0
	inSpace := false
	for i, r := range text {
		sp := unicode.IsSpace(r)
		if inSpace && !sp {
			units = append(units, text[start:i])
			start = i
		}
		inSpace = sp
	}
	return append(units, text[start:])
}

// hardSplit cuts text into pieces of at most max runes.
func hardSplit(text string, max int) []string {
	runes := []rune(text)
	var out []string
	for len(runes) > 0 {
		n := min(max, len(runes))
		if t := strings.TrimSpace(string(runes[:n])); t != "" {
			out = append(out, t)
		}
		runes = runes[n:]
	}
	return out
}
