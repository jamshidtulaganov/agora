// Package extract turns an uploaded business document (Markdown, plain text,
// Word .docx, Excel .xlsx, CSV, PDF) into a flat list of structured blocks —
// headings, paragraphs, lists and tables — that keep where each piece came
// from (page for PDFs, sheet for spreadsheets). It has no database or network
// dependencies; PDFs are read by shelling out to poppler's pdftotext.
package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"
	"unicode/utf8"
)

// Block kinds.
const (
	KindHeading   = "heading"
	KindParagraph = "paragraph"
	KindList      = "list"
	KindTable     = "table"
)

// Limits. Inputs over these are rejected with ErrTooLarge.
const (
	// MaxInputBytes caps the uploaded file size.
	MaxInputBytes = 25 << 20
	// MaxTextChars caps the total extracted text (runes) across all blocks.
	MaxTextChars = 500_000
	// MaxUnzippedBytes caps the declared uncompressed size of a .docx/.xlsx.
	MaxUnzippedBytes = 100 << 20
	// MaxZipEntries caps the number of entries in a .docx/.xlsx archive.
	MaxZipEntries = 10_000
	// MaxSheetRows caps the data rows (below the header) of one sheet or CSV.
	MaxSheetRows = 20_000
	// PDFTimeout bounds one pdftotext run.
	PDFTimeout = 60 * time.Second
)

var (
	// ErrUnsupported is returned for file types Extract cannot read.
	ErrUnsupported = errors.New("extract: unsupported file type")
	// ErrTooLarge is returned when the input or its extracted text is over a limit.
	ErrTooLarge = errors.New("extract: document too large")
	// ErrNotUTF8 is returned for text formats (.md, .txt, .csv) that are not UTF-8/UTF-16.
	ErrNotUTF8 = errors.New("extract: file is not valid UTF-8 text; save it with UTF-8 encoding")
	// ErrPDFToolMissing is returned when pdftotext is not installed.
	ErrPDFToolMissing = errors.New("PDF support needs poppler's pdftotext installed on the server")
)

// Block is one structural unit of a document, in reading order.
type Block struct {
	Kind     string // KindHeading | KindParagraph | KindList | KindTable
	Level    int    // heading level 1-6 (headings only)
	Text     string // heading text; paragraph text; list as markdown ("- item" / "1. item" lines)
	Table    *Table // tables only
	Location string // "p. 3" for PDFs, "Sheet \"Rates\"" for spreadsheets, "" otherwise
}

// Table is a table block's cells. Header is the first row of the source table.
type Table struct {
	Header   []string
	Rows     [][]string
	Sheet    string // spreadsheet sheet name, "" otherwise
	FirstRow int    // 1-based source row of Rows[0] (spreadsheets/csv), 0 otherwise
}

// Document is the extraction result.
type Document struct {
	Title     string // never empty
	Blocks    []Block
	PageCount int  // PDFs
	Scanned   bool // PDF with (almost) no text layer — needs OCR
}

type format int

const (
	formatMarkdown format = iota + 1
	formatText
	formatDOCX
	formatXLSX
	formatCSV
	formatPDF
)

// Extract reads data according to filename's extension (or, when the
// extension is unknown, contentType) and returns its blocks.
func Extract(ctx context.Context, filename, contentType string, data []byte) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	if len(data) > MaxInputBytes {
		return Document{}, fmt.Errorf("%w: file is %d bytes, the limit is %d", ErrTooLarge, len(data), MaxInputBytes)
	}
	f, err := detectFormat(filename, contentType)
	if err != nil {
		return Document{}, err
	}
	b := &builder{ctx: ctx}
	var doc Document
	switch f {
	case formatMarkdown:
		doc, err = extractMarkdown(data, b)
	case formatText:
		doc, err = extractText(data, b)
	case formatDOCX:
		doc, err = extractDOCX(ctx, data, b)
	case formatXLSX:
		doc, err = extractXLSX(ctx, data, b)
	case formatCSV:
		doc, err = extractCSV(ctx, data, b)
	case formatPDF:
		doc, err = extractPDF(ctx, data, b)
	}
	if err != nil {
		return Document{}, err
	}
	doc.Blocks = b.blocks
	doc.Title = strings.TrimSpace(doc.Title)
	if doc.Title == "" {
		doc.Title = titleFromFilename(filename)
	}
	return doc, nil
}

func detectFormat(filename, contentType string) (format, error) {
	switch ext := strings.ToLower(fileExt(filename)); ext {
	case ".md", ".markdown":
		return formatMarkdown, nil
	case ".txt", ".text":
		return formatText, nil
	case ".docx":
		return formatDOCX, nil
	case ".xlsx":
		return formatXLSX, nil
	case ".csv", ".tsv":
		return formatCSV, nil
	case ".pdf":
		return formatPDF, nil
	case ".doc":
		return 0, fmt.Errorf("%w: legacy Word .doc files are not supported; save the file as .docx", ErrUnsupported)
	case ".xls":
		return 0, fmt.Errorf("%w: legacy Excel .xls files are not supported; save the file as .xlsx", ErrUnsupported)
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(contentType))
	}
	switch mt {
	case "text/markdown", "text/x-markdown":
		return formatMarkdown, nil
	case "text/plain":
		return formatText, nil
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return formatDOCX, nil
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return formatXLSX, nil
	case "text/csv", "application/csv", "text/comma-separated-values", "text/tab-separated-values":
		return formatCSV, nil
	case "application/pdf":
		return formatPDF, nil
	}
	return 0, fmt.Errorf("%w: %q (%s)", ErrUnsupported, baseName(filename), contentType)
}

// builder accumulates blocks and enforces the extracted-text cap.
type builder struct {
	ctx    context.Context
	blocks []Block
	chars  int
}

// charge counts n runes of extracted text against MaxTextChars.
func (b *builder) charge(n int) error {
	b.chars += n
	if b.chars > MaxTextChars {
		return fmt.Errorf("%w: more than %d characters of text", ErrTooLarge, MaxTextChars)
	}
	return nil
}

// add appends a non-table block (or a table not charged yet) and charges its text.
func (b *builder) add(blk Block) error {
	n := utf8.RuneCountInString(blk.Text)
	if blk.Table != nil {
		n += tableChars(blk.Table)
	}
	if err := b.charge(n); err != nil {
		return err
	}
	b.blocks = append(b.blocks, blk)
	return nil
}

// addCharged appends a block whose text was already charged incrementally.
func (b *builder) addCharged(blk Block) {
	b.blocks = append(b.blocks, blk)
}

func (b *builder) addHeading(level int, text, loc string) error {
	text = strings.TrimSpace(collapseSpaces(strings.ReplaceAll(text, "\n", " ")))
	if text == "" {
		return nil
	}
	return b.add(Block{Kind: KindHeading, Level: clampLevel(level), Text: text, Location: loc})
}

func (b *builder) addText(kind, text, loc string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return b.add(Block{Kind: kind, Text: text, Location: loc})
}

func tableChars(t *Table) int {
	n := 0
	for _, c := range t.Header {
		n += utf8.RuneCountInString(c)
	}
	for _, r := range t.Rows {
		for _, c := range r {
			n += utf8.RuneCountInString(c)
		}
	}
	return n
}

func clampLevel(l int) int {
	if l < 1 {
		return 1
	}
	if l > 6 {
		return 6
	}
	return l
}

// decodeText returns data as a UTF-8 string with a leading BOM removed and
// newlines normalized to "\n". UTF-16 with a BOM is transcoded; anything else
// must be valid UTF-8.
func decodeText(data []byte) (string, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return normalizeNewlines(decodeUTF16(data[2:], false)), nil
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return normalizeNewlines(decodeUTF16(data[2:], true)), nil
	}
	if !utf8.Valid(data) {
		return "", ErrNotUTF8
	}
	return normalizeNewlines(string(data)), nil
}

func decodeUTF16(b []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	var sb strings.Builder
	sb.Grow(len(units))
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u >= 0xD800 && u < 0xDC00 && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] < 0xE000:
			sb.WriteRune(rune(u-0xD800)<<10 | rune(units[i+1]-0xDC00) + 0x10000)
			i++
		case u >= 0xD800 && u < 0xE000:
			sb.WriteRune(utf8.RuneError)
		default:
			sb.WriteRune(rune(u))
		}
	}
	return sb.String()
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// sanitize makes extracted text safe to store: valid UTF-8, no NUL (Postgres
// text rejects it) and no C0 control characters other than tab and newline.
func sanitize(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n':
			return r
		case r < 0x20 || r == 0x7F || r == '\uFEFF':
			return -1
		}
		return r
	}, s)
}

// collapseSpaces replaces runs of spaces and tabs with a single space.
func collapseSpaces(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\u00A0' {
			if !space {
				sb.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		sb.WriteRune(r)
	}
	return sb.String()
}

func baseName(filename string) string {
	if i := strings.LastIndexAny(filename, `/\`); i >= 0 {
		return filename[i+1:]
	}
	return filename
}

func fileExt(filename string) string {
	name := baseName(filename)
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[i:]
	}
	return ""
}

func titleFromFilename(filename string) string {
	name := baseName(filename)
	name = strings.TrimSuffix(name, fileExt(name))
	name = strings.TrimSpace(name)
	if name == "" {
		return "Untitled"
	}
	return name
}
