package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	cfbMagic        = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	headingStyleRe  = regexp.MustCompile(`^heading ([1-9])$`)
	headingStyleID  = regexp.MustCompile(`^Heading([1-9])$`)
	tocStyleNameRe  = regexp.MustCompile(`^toc (heading|[1-9])$`)
	fakeHeadingStop = ".,;!?"
)

// unsetCounter marks a list level that has not produced an item yet.
const unsetCounter = math.MinInt

// openZip opens a .docx/.xlsx archive after checking the zip-bomb limits
// against its central directory. archive/zip refuses to inflate an entry past
// its declared size, so the declared sizes are a sound bound.
func openZip(data []byte, kind string) (*zip.Reader, error) {
	if bytes.HasPrefix(data, cfbMagic) {
		return nil, fmt.Errorf("%w: this %s is password-protected or in a legacy binary format; save it as a regular %s", ErrUnsupported, kind, kind)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("extract: not a valid %s file: %w", kind, err)
	}
	if len(zr.File) > MaxZipEntries {
		return nil, fmt.Errorf("%w: archive has %d entries, the limit is %d", ErrTooLarge, len(zr.File), MaxZipEntries)
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if total > MaxUnzippedBytes || f.UncompressedSize64 > MaxUnzippedBytes {
			return nil, fmt.Errorf("%w: archive unpacks to more than %d bytes", ErrTooLarge, MaxUnzippedBytes)
		}
	}
	return zr, nil
}

// readZipEntry returns the named entry's content, or nil when it is absent.
func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if !strings.EqualFold(f.Name, name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("extract: open %s: %w", name, err)
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, MaxUnzippedBytes+1))
		if err != nil {
			return nil, fmt.Errorf("extract: read %s: %w", name, err)
		}
		if len(data) > MaxUnzippedBytes {
			return nil, fmt.Errorf("%w: %s unpacks to more than %d bytes", ErrTooLarge, name, MaxUnzippedBytes)
		}
		return data, nil
	}
	return nil, nil
}

// ---- OOXML part models (namespace-agnostic: struct tags match local names) ----

type xVal struct {
	Val string `xml:"val,attr"`
}

type xNumPr struct {
	ILvl  *xVal `xml:"ilvl"`
	NumID *xVal `xml:"numId"`
}

type xPPr struct {
	PStyle     *xVal   `xml:"pStyle"`
	OutlineLvl *xVal   `xml:"outlineLvl"`
	NumPr      *xNumPr `xml:"numPr"`
}

type xStyles struct {
	Styles []struct {
		Type    string `xml:"type,attr"`
		ID      string `xml:"styleId,attr"`
		Name    xVal   `xml:"name"`
		BasedOn *xVal  `xml:"basedOn"`
		PPr     xPPr   `xml:"pPr"`
	} `xml:"style"`
}

type xNumbering struct {
	Abstract []struct {
		ID   string `xml:"abstractNumId,attr"`
		Lvls []struct {
			ILvl   string `xml:"ilvl,attr"`
			Start  *xVal  `xml:"start"`
			NumFmt *xVal  `xml:"numFmt"`
		} `xml:"lvl"`
	} `xml:"abstractNum"`
	Nums []struct {
		ID       string `xml:"numId,attr"`
		Abstract xVal   `xml:"abstractNumId"`
	} `xml:"num"`
}

type xCoreProps struct {
	Title string `xml:"title"`
}

type docxStyle struct {
	name    string // lowercased
	basedOn string
	outline int // -1 when unset
	numID   string
	ilvl    int
	hasNum  bool
}

type docxLevel struct {
	format string
	start  int
}

type docxReader struct {
	ctx      context.Context
	dec      *xml.Decoder
	styles   map[string]docxStyle
	levels   map[string]map[int]docxLevel // numId → ilvl → level
	counters map[string][]int             // numId → running counters per ilvl
	tokens   int
}

type docxPara struct {
	text    string
	heading int // 1-6, 0 = not a heading
	title   bool
	toc     bool
	numID   string
	ilvl    int
	bold    bool // every text run is bold
}

type docxItem struct {
	para  *docxPara
	table *Table
}

func extractDOCX(ctx context.Context, data []byte, b *builder) (Document, error) {
	zr, err := openZip(data, "Word document")
	if err != nil {
		return Document{}, err
	}
	main, err := readZipEntry(zr, "word/document.xml")
	if err != nil {
		return Document{}, err
	}
	if main == nil {
		return Document{}, errors.New("extract: not a Word document (word/document.xml is missing)")
	}
	r := &docxReader{ctx: ctx, counters: map[string][]int{}}
	if r.styles, err = loadDocxStyles(zr); err != nil {
		return Document{}, err
	}
	if r.levels, err = loadDocxNumbering(zr); err != nil {
		return Document{}, err
	}
	items, err := r.parseDocument(main)
	if err != nil {
		return Document{}, err
	}
	coreTitle := ""
	if core, err := readZipEntry(zr, "docProps/core.xml"); err == nil && core != nil {
		var cp xCoreProps
		if xml.Unmarshal(core, &cp) == nil {
			coreTitle = strings.TrimSpace(sanitize(cp.Title))
		}
	}
	title, err := r.emit(items, b)
	if err != nil {
		return Document{}, err
	}
	if title == "" {
		title = coreTitle
	}
	if title == "" {
		for _, blk := range b.blocks {
			if blk.Kind == KindHeading && blk.Level == 1 {
				title = blk.Text
				break
			}
		}
	}
	return Document{Title: title}, nil
}

func loadDocxStyles(zr *zip.Reader) (map[string]docxStyle, error) {
	data, err := readZipEntry(zr, "word/styles.xml")
	if err != nil || data == nil {
		return map[string]docxStyle{}, err
	}
	var xs xStyles
	if err := xml.Unmarshal(data, &xs); err != nil {
		return nil, fmt.Errorf("extract: parse word/styles.xml: %w", err)
	}
	out := make(map[string]docxStyle, len(xs.Styles))
	for _, s := range xs.Styles {
		if s.Type != "" && s.Type != "paragraph" {
			continue
		}
		st := docxStyle{name: strings.ToLower(strings.TrimSpace(s.Name.Val)), outline: -1}
		if s.BasedOn != nil {
			st.basedOn = s.BasedOn.Val
		}
		if s.PPr.OutlineLvl != nil {
			if n, err := strconv.Atoi(s.PPr.OutlineLvl.Val); err == nil {
				st.outline = n
			}
		}
		if np := s.PPr.NumPr; np != nil && np.NumID != nil {
			st.hasNum = true
			st.numID = np.NumID.Val
			if np.ILvl != nil {
				st.ilvl, _ = strconv.Atoi(np.ILvl.Val)
			}
		}
		out[s.ID] = st
	}
	return out, nil
}

func loadDocxNumbering(zr *zip.Reader) (map[string]map[int]docxLevel, error) {
	data, err := readZipEntry(zr, "word/numbering.xml")
	if err != nil || data == nil {
		return map[string]map[int]docxLevel{}, err
	}
	var xn xNumbering
	if err := xml.Unmarshal(data, &xn); err != nil {
		return nil, fmt.Errorf("extract: parse word/numbering.xml: %w", err)
	}
	abstract := map[string]map[int]docxLevel{}
	for _, a := range xn.Abstract {
		lv := map[int]docxLevel{}
		for _, l := range a.Lvls {
			ilvl, err := strconv.Atoi(l.ILvl)
			if err != nil {
				continue
			}
			d := docxLevel{format: "decimal", start: 1}
			if l.NumFmt != nil {
				d.format = l.NumFmt.Val
			}
			if l.Start != nil {
				if n, err := strconv.Atoi(l.Start.Val); err == nil {
					d.start = n
				}
			}
			lv[ilvl] = d
		}
		abstract[a.ID] = lv
	}
	out := map[string]map[int]docxLevel{}
	for _, n := range xn.Nums {
		if lv, ok := abstract[n.Abstract.Val]; ok {
			out[n.ID] = lv
		}
	}
	return out, nil
}

// token reads the next XML token, checking for cancellation periodically.
func (r *docxReader) token() (xml.Token, error) {
	r.tokens++
	if r.tokens%4096 == 0 {
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
	}
	return r.dec.Token()
}

func (r *docxReader) parseDocument(data []byte) ([]docxItem, error) {
	r.dec = xml.NewDecoder(bytes.NewReader(data))
	var items []docxItem
	for {
		tok, err := r.token()
		if err == io.EOF {
			return items, nil
		}
		if err != nil {
			return nil, fmt.Errorf("extract: parse word/document.xml: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "p":
			p, err := r.parseParagraph(start)
			if err != nil {
				return nil, err
			}
			items = append(items, docxItem{para: p})
		case "tbl":
			t, err := r.parseTable(start)
			if err != nil {
				return nil, err
			}
			items = append(items, docxItem{table: t})
		case "sectPr", "sdtPr", "sdtEndPr", "drawing", "pict", "object", "Fallback", "del", "moveFrom":
			if err := r.dec.Skip(); err != nil {
				return nil, err
			}
		}
		// Anything else (body, sdt, sdtContent, customXml, …) is a container:
		// keep reading its children.
	}
}

// parseParagraph consumes a w:p element.
func (r *docxReader) parseParagraph(start xml.StartElement) (*docxPara, error) {
	var sb strings.Builder
	var ppr xPPr
	allBold, hasText := true, false
	for {
		tok, err := r.token()
		if err != nil {
			return nil, fmt.Errorf("extract: parse paragraph: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "pPr":
				if err := r.dec.DecodeElement(&ppr, &t); err != nil {
					return nil, err
				}
			case "r":
				text, bold, err := r.parseRun(t)
				if err != nil {
					return nil, err
				}
				if strings.TrimSpace(text) != "" {
					hasText = true
					allBold = allBold && bold
				}
				sb.WriteString(text)
			case "drawing", "pict", "object", "Fallback", "del", "moveFrom", "sdtPr", "sdtEndPr",
				"footnoteReference", "endnoteReference", "commentReference":
				if err := r.dec.Skip(); err != nil {
					return nil, err
				}
			}
			// Other elements (hyperlink, ins, smartTag, fldSimple, sdt, …)
			// wrap runs: keep reading their children.
		case xml.EndElement:
			if t.Name == start.Name {
				return r.resolveParagraph(sb.String(), ppr, hasText && allBold), nil
			}
		}
	}
}

// parseRun consumes a w:r (or m:r) element and returns its text.
func (r *docxReader) parseRun(start xml.StartElement) (string, bool, error) {
	var sb strings.Builder
	bold := false
	for {
		tok, err := r.token()
		if err != nil {
			return "", false, fmt.Errorf("extract: parse run: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "rPr":
				var rp struct {
					B *xVal `xml:"b"`
				}
				if err := r.dec.DecodeElement(&rp, &t); err != nil {
					return "", false, err
				}
				bold = rp.B != nil && rp.B.Val != "0" && rp.B.Val != "false" && rp.B.Val != "off"
				continue
			case "t":
				var s string
				if err := r.dec.DecodeElement(&s, &t); err != nil {
					return "", false, err
				}
				sb.WriteString(s)
				continue
			case "tab", "ptab":
				sb.WriteByte(' ')
			case "br", "cr":
				sb.WriteByte('\n')
			case "noBreakHyphen":
				sb.WriteByte('-')
			}
			if err := r.dec.Skip(); err != nil {
				return "", false, err
			}
		case xml.EndElement:
			if t.Name == start.Name {
				return sb.String(), bold, nil
			}
		}
	}
}

// parseTable consumes a w:tbl element. The first row becomes the header.
// Horizontally merged cells (gridSpan) are padded with empty cells so columns
// stay aligned; vertically merged continuation cells repeat the value above,
// so each row reads on its own once rows are split into chunks.
func (r *docxReader) parseTable(start xml.StartElement) (*Table, error) {
	var rows [][]string
	var row []string
	for {
		tok, err := r.token()
		if err != nil {
			return nil, fmt.Errorf("extract: parse table: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tr":
				row = []string{}
			case "tc":
				text, span, cont, err := r.parseCell(t)
				if err != nil {
					return nil, err
				}
				col := len(row)
				if cont && len(rows) > 0 && col < len(rows[len(rows)-1]) {
					text = rows[len(rows)-1][col]
				}
				row = append(row, text)
				for k := 1; k < span; k++ {
					row = append(row, "")
				}
			case "tblPr", "tblGrid", "trPr", "tblPrEx", "sdtPr", "sdtEndPr":
				if err := r.dec.Skip(); err != nil {
					return nil, err
				}
			}
		case xml.EndElement:
			switch {
			case t.Name.Local == "tr" && row != nil:
				rows = append(rows, row)
				row = nil
			case t.Name == start.Name:
				tbl := &Table{}
				if len(rows) > 0 {
					tbl.Header = rows[0]
					tbl.Rows = rows[1:]
				}
				return tbl, nil
			}
		}
	}
}

// parseCell consumes a w:tc element: its text (paragraphs joined by newlines,
// nested tables flattened), its gridSpan, and whether it continues a
// vertical merge.
func (r *docxReader) parseCell(start xml.StartElement) (string, int, bool, error) {
	var parts []string
	span, cont := 1, false
	for {
		tok, err := r.token()
		if err != nil {
			return "", 0, false, fmt.Errorf("extract: parse table cell: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tcPr":
				var pr struct {
					GridSpan *xVal `xml:"gridSpan"`
					VMerge   *xVal `xml:"vMerge"`
				}
				if err := r.dec.DecodeElement(&pr, &t); err != nil {
					return "", 0, false, err
				}
				if pr.GridSpan != nil {
					if n, err := strconv.Atoi(pr.GridSpan.Val); err == nil && n > 1 && n <= 64 {
						span = n
					}
				}
				cont = pr.VMerge != nil && pr.VMerge.Val != "restart"
			case "p":
				p, err := r.parseParagraph(t)
				if err != nil {
					return "", 0, false, err
				}
				if s := strings.TrimSpace(collapseSpaces(p.text)); s != "" {
					parts = append(parts, s)
				}
			case "tbl":
				nested, err := r.parseTable(t)
				if err != nil {
					return "", 0, false, err
				}
				for _, row := range append([][]string{nested.Header}, nested.Rows...) {
					if s := strings.TrimSpace(strings.Join(row, " ")); s != "" {
						parts = append(parts, s)
					}
				}
			case "sdtPr", "sdtEndPr":
				if err := r.dec.Skip(); err != nil {
					return "", 0, false, err
				}
			}
		case xml.EndElement:
			if t.Name == start.Name {
				return sanitize(strings.Join(parts, "\n")), span, cont, nil
			}
		}
	}
}

// resolveParagraph applies the paragraph's style chain: heading level (style
// name "heading N", "Title", or outline level), list numbering, TOC entries.
func (r *docxReader) resolveParagraph(text string, ppr xPPr, bold bool) *docxPara {
	p := &docxPara{text: sanitize(text), bold: bold}
	styleID := ""
	if ppr.PStyle != nil {
		styleID = ppr.PStyle.Val
	}
	outline := -1
	numFound := false
	seen := map[string]bool{}
	for id, depth := styleID, 0; id != "" && depth < 16 && !seen[id]; depth++ {
		seen[id] = true
		st, ok := r.styles[id]
		if !ok {
			if m := headingStyleID.FindStringSubmatch(id); m != nil && p.heading == 0 {
				p.heading, _ = strconv.Atoi(m[1])
			} else if id == "Title" {
				p.title = true
			}
			break
		}
		if depth == 0 && tocStyleNameRe.MatchString(st.name) {
			p.toc = true
		}
		if p.heading == 0 && !p.title {
			if m := headingStyleRe.FindStringSubmatch(st.name); m != nil {
				p.heading, _ = strconv.Atoi(m[1])
			} else if st.name == "title" {
				p.title = true
			}
		}
		if outline < 0 && st.outline >= 0 {
			outline = st.outline
		}
		if !numFound && st.hasNum {
			numFound = true
			p.numID, p.ilvl = st.numID, st.ilvl
		}
		id = st.basedOn
	}
	if ppr.OutlineLvl != nil {
		if n, err := strconv.Atoi(ppr.OutlineLvl.Val); err == nil {
			outline = n
		}
	}
	// Outline level 9 means body text; 0-8 map to heading levels 1-9.
	if p.heading == 0 && !p.title && outline >= 0 && outline <= 8 {
		p.heading = outline + 1
	}
	p.heading = min(p.heading, 6)
	if np := ppr.NumPr; np != nil {
		if np.NumID != nil {
			p.numID = np.NumID.Val
		}
		if np.ILvl != nil {
			p.ilvl, _ = strconv.Atoi(np.ILvl.Val)
		}
	}
	if p.numID == "0" {
		p.numID = ""
	}
	if p.ilvl < 0 || p.ilvl > 8 {
		p.ilvl = 0
	}
	return p
}

// emit turns parsed items into blocks and returns the Title-style paragraph's
// text, if any.
func (r *docxReader) emit(items []docxItem, b *builder) (string, error) {
	styledHeadings := false
	for _, it := range items {
		if it.para != nil && (it.para.heading > 0 || it.para.title) && strings.TrimSpace(it.para.text) != "" {
			styledHeadings = true
			break
		}
	}
	title := ""
	var list []string
	flushList := func() error {
		if len(list) == 0 {
			return nil
		}
		text := strings.Join(list, "\n")
		list = nil
		return b.addText(KindList, text, "")
	}
	for i, it := range items {
		if it.table != nil {
			if err := flushList(); err != nil {
				return "", err
			}
			if len(it.table.Header) == 0 && len(it.table.Rows) == 0 {
				continue
			}
			if err := b.add(Block{Kind: KindTable, Table: it.table}); err != nil {
				return "", err
			}
			continue
		}
		p := it.para
		text := strings.TrimSpace(collapseSpaces(p.text))
		if text == "" || p.toc {
			continue
		}
		switch {
		case p.title || p.heading > 0:
			if err := flushList(); err != nil {
				return "", err
			}
			level := p.heading
			if p.title {
				level = 1
				if title == "" {
					title = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
				}
			}
			if err := b.addHeading(level, text, ""); err != nil {
				return "", err
			}
		case p.numID != "":
			indent := strings.Repeat("  ", p.ilvl)
			item := strings.ReplaceAll(text, "\n", " ")
			list = append(list, indent+r.marker(p.numID, p.ilvl)+" "+item)
		case !styledHeadings && looksLikeBoldHeading(p, text) && nextIsBody(items, i):
			if err := flushList(); err != nil {
				return "", err
			}
			if err := b.addHeading(2, text, ""); err != nil {
				return "", err
			}
		default:
			if err := flushList(); err != nil {
				return "", err
			}
			if err := b.addText(KindParagraph, text, ""); err != nil {
				return "", err
			}
		}
	}
	return title, flushList()
}

// looksLikeBoldHeading reports whether a paragraph is a manual heading: a
// short, fully bold line without sentence punctuation. Only used for
// documents that have no styled headings at all.
func looksLikeBoldHeading(p *docxPara, text string) bool {
	if !p.bold || p.numID != "" || strings.ContainsRune(text, '\n') {
		return false
	}
	if n := utf8.RuneCountInString(text); n > 120 || !hasLetter(text) {
		return false
	}
	last, _ := utf8.DecodeLastRuneInString(text)
	return !strings.ContainsRune(fakeHeadingStop, last)
}

// nextIsBody reports whether the next non-empty item after i is body content
// (a paragraph that is not itself a manual heading, a list item, or a table).
func nextIsBody(items []docxItem, i int) bool {
	for _, it := range items[i+1:] {
		if it.table != nil {
			return true
		}
		text := strings.TrimSpace(collapseSpaces(it.para.text))
		if text == "" || it.para.toc {
			continue
		}
		return it.para.numID != "" || !looksLikeBoldHeading(it.para, text)
	}
	return false
}

// marker returns the list marker for the next item at (numID, ilvl) and
// advances Word's running counters: deeper levels restart after a shallower
// item.
func (r *docxReader) marker(numID string, ilvl int) string {
	lv, ok := r.levels[numID][ilvl]
	if !ok || lv.format == "bullet" || lv.format == "none" || lv.format == "" {
		return "-"
	}
	c := r.counters[numID]
	for len(c) <= ilvl {
		c = append(c, unsetCounter)
	}
	if c[ilvl] == unsetCounter {
		c[ilvl] = lv.start
	} else {
		c[ilvl]++
	}
	for k := ilvl + 1; k < len(c); k++ {
		c[k] = unsetCounter
	}
	r.counters[numID] = c
	return formatListNumber(c[ilvl], lv.format) + "."
}

func formatListNumber(n int, format string) string {
	switch format {
	case "lowerLetter":
		return letters(n, 'a')
	case "upperLetter":
		return letters(n, 'A')
	case "lowerRoman":
		return strings.ToLower(roman(n))
	case "upperRoman":
		return roman(n)
	}
	return strconv.Itoa(n)
}

func letters(n int, base rune) string {
	if n < 1 {
		return strconv.Itoa(n)
	}
	// Word repeats the letter past z: aa, bb, …
	return strings.Repeat(string(base+rune((n-1)%26)), (n-1)/26+1)
}

func roman(n int) string {
	if n < 1 || n > 3999 {
		return strconv.Itoa(n)
	}
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	var sb strings.Builder
	for i, v := range vals {
		for n >= v {
			sb.WriteString(syms[i])
			n -= v
		}
	}
	return sb.String()
}
