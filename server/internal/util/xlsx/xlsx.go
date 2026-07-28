// Package xlsx writes the narrow slice of the XLSX format these reports need:
// one sheet, inline strings, numeric cells, a bold header row.
//
// Hand-rolled on the standard library rather than pulling in a spreadsheet
// dependency. An .xlsx file is a zip of a handful of fixed XML parts, and the
// subset written here does not vary — there are no formulas, merges, charts or
// multiple sheets to get wrong. A general library would be a large surface for
// a format we emit exactly one shape of.
//
// The point of shipping a spreadsheet rather than a document is that the
// numbers stay NUMBERS: a reader can sort a column, sum it, or chart it. So a
// cell that parses as a number is written as one, and only the rest becomes
// text.
package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Style selects one of the workbook's predefined cell formats. Styles are a
// closed set rather than free-form formatting: a report has a handful of roles
// — title, section, table header, table cell — and letting each caller invent
// its own font would produce a different-looking file every week.
//
// The values are indices into cellXfs, so their ORDER must match the order
// they are written in styles.xml.
type Style int

const (
	StyleNormal Style = iota
	StyleTitle
	StyleSection
	StyleTableHeader
	StyleTableText
	StyleTableInt
	StyleTableDecimal
	StyleStrong
	StyleTableTextBand
	StyleTableIntBand
	StyleTableDecimalBand
)

// Banded returns the alternating-fill variant of a table style, or the style
// unchanged when it has none.
func (s Style) Banded() Style {
	switch s {
	case StyleTableText:
		return StyleTableTextBand
	case StyleTableInt:
		return StyleTableIntBand
	case StyleTableDecimal:
		return StyleTableDecimalBand
	default:
		return s
	}
}

// Cell is one value. Number wins when Numeric is true; otherwise Text is used.
type Cell struct {
	Text    string
	Number  float64
	Numeric bool
	// Style is applied per cell rather than per row: a report block's first row
	// is a header while the prose line above it is not.
	Style Style
}

// With returns the cell restyled. Lets a caller build a value with AutoCell and
// then place it — the value's type and its role in the sheet are separate
// decisions.
func (c Cell) With(s Style) Cell {
	c.Style = s
	return c
}

// Text returns a text cell.
func TextCell(s string) Cell { return Cell{Text: s} }

// HeaderCell returns a table-header cell.
func HeaderCell(s string) Cell { return Cell{Text: s, Style: StyleTableHeader} }

// NumberCell returns a numeric cell.
func NumberCell(f float64) Cell { return Cell{Number: f, Numeric: true} }

// TableCell styles a value for use inside a table: bordered, with numbers
// right-aligned and thousands-separated. Whole numbers and decimals get
// different formats so a count does not render as "360.00".
func TableCell(c Cell) Cell {
	if !c.Numeric {
		return c.With(StyleTableText)
	}
	if c.Number == math.Trunc(c.Number) {
		return c.With(StyleTableInt)
	}
	return c.With(StyleTableDecimal)
}

// AutoCell returns a numeric cell when the string parses as a number, and a
// text cell otherwise. Thousands separators and the surrounding whitespace a
// markdown table carries are stripped first — "2 417" is a number to a reader
// and must be one to the spreadsheet too.
func AutoCell(s string) Cell {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return TextCell("")
	}
	cleaned := strings.NewReplacer(" ", "", " ", "", ",", "").Replace(trimmed)
	// A lone "-" or "—" is a placeholder for "no value", not a negative number.
	if cleaned == "-" || cleaned == "—" {
		return TextCell(trimmed)
	}
	if f, err := strconv.ParseFloat(cleaned, 64); err == nil {
		return NumberCell(f)
	}
	return TextCell(trimmed)
}

// Sheet is one worksheet: a name and its rows.
type Sheet struct {
	Name string
	Rows [][]Cell
	// FreezeHeaderRow freezes everything above the given 1-based row, so a
	// table's header stays visible while the reader scrolls. Zero disables it.
	// Only meaningful on a sheet holding ONE table — freezing a mid-sheet row
	// on a sheet with several would pin an arbitrary one.
	FreezeHeaderRow int
	// AutoFilterRange is the A1 range the filter dropdowns cover, e.g.
	// "A1:C12". This is what turns a report into something a reader can
	// interrogate: sort by count, filter to one person, without touching the
	// source. Empty disables it.
	AutoFilterRange string
	// ColWidths sets per-column width in Excel's character units. Empty means
	// the default width, which is almost never right: a label like
	// "Median yopilish (kun)" is clipped to "Median yo" and the reader cannot
	// tell what they are looking at. Use AutoWidths to derive them.
	ColWidths []float64
}

// Width bounds, in Excel's character units.
const (
	// minColWidth keeps a narrow numeric column from collapsing to a sliver.
	minColWidth = 9
	// maxColWidth stops one long sentence from pushing a column off-screen.
	// Text past it still displays: Excel overflows into empty neighbours.
	maxColWidth = 46
	// widthPadding covers the fact that Excel's character unit is based on the
	// default font's digit width, so proportional text needs a little slack.
	widthPadding = 2.5
)

// AutoWidths derives column widths from cell content.
//
// Only rows with more than one cell are measured — those are the tables. A
// single-cell row is prose (a heading, the headline, a bullet), and measuring
// it would stretch the first column to sentence length, leaving every table on
// the sheet with one absurdly wide label column.
//
// Prose is not lost by this: Excel spills a long value into adjacent cells when
// they are empty, which is exactly the case for a one-cell row.
func AutoWidths(rows [][]Cell) []float64 {
	widths := []float64{}
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		for i, cell := range row {
			for len(widths) <= i {
				widths = append(widths, minColWidth)
			}
			text := cell.Text
			if cell.Numeric {
				text = strconv.FormatFloat(cell.Number, 'f', -1, 64)
			}
			if w := float64(len([]rune(text))) + widthPadding; w > widths[i] {
				widths[i] = w
			}
		}
	}
	for i := range widths {
		if widths[i] > maxColWidth {
			widths[i] = maxColWidth
		}
	}
	return widths
}

// ColumnName is columnName for callers building an A1 range.
func ColumnName(i int) string { return columnName(i) }

// columnName converts a zero-based index to a spreadsheet column: 0→A, 25→Z,
// 26→AA. Reports are narrow, but a by-month table plus a total column already
// reaches into the teens and a wrong letter past Z would corrupt the sheet
// silently rather than failing.
func columnName(i int) string {
	name := ""
	for i >= 0 {
		name = string(rune('A'+i%26)) + name
		i = i/26 - 1
	}
	return name
}

// escape XML-escapes cell text. Agent-authored strings reach this directly, so
// an unescaped "&" or "<" would produce a file Excel refuses to open.
func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// sanitizeSheetName enforces Excel's rules: at most 31 characters, and none of
// : \ / ? * [ ]. A violation makes the whole workbook unopenable, so it is
// corrected rather than reported.
func sanitizeSheetName(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	if cleaned == "" {
		cleaned = "Sheet1"
	}
	runes := []rune(cleaned)
	if len(runes) > 31 {
		cleaned = string(runes[:31])
	}
	return cleaned
}

// Write builds the workbook. Returns the complete .xlsx bytes.
func Write(sheets []Sheet) ([]byte, error) {
	if len(sheets) == 0 {
		sheets = []Sheet{{Name: "Sheet1"}}
	}
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	add := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}

	var contentTypes, workbookSheets, workbookRels strings.Builder
	contentTypes.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
		`<Default Extension="xml" ContentType="application/xml"/>` +
		`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`)

	for i, sheet := range sheets {
		n := i + 1
		part := fmt.Sprintf("xl/worksheets/sheet%d.xml", n)
		contentTypes.WriteString(fmt.Sprintf(
			`<Override PartName="/%s" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, part))
		workbookSheets.WriteString(fmt.Sprintf(
			`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escape(sanitizeSheetName(sheet.Name)), n, n))
		workbookRels.WriteString(fmt.Sprintf(
			`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n, n))
		if err := add(part, sheetXML(sheet)); err != nil {
			return nil, err
		}
	}
	contentTypes.WriteString(`</Types>`)

	styles := reportStyles()

	parts := map[string]string{
		"[Content_Types].xml": contentTypes.String(),
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`,
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets>` + workbookSheets.String() + `</sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			workbookRels.String() +
			`<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` +
			`</Relationships>`,
		"xl/styles.xml": styles,
	}
	for name, content := range parts {
		if err := add(name, content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Report palette. Kept here, in one place, so every workbook this package
// produces looks like the same document rather than a fresh improvisation.
const (
	brandHex = "2563EB" // Agora royal blue — titles and table headers
	inkHex   = "1F2937" // body text; pure black reads as harsh on screen
	ruleHex  = "D1D5DB" // table borders, light enough not to fight the numbers
	bandHex  = "F3F6FB" // alternating row tint; barely there by design
)

// reportStyles builds styles.xml. Excel requires the full
// fonts/fills/borders/cellStyleXfs scaffolding even where most of it is empty,
// and it requires fills[0]=none and fills[1]=gray125 in exactly those slots —
// a solid fill placed at index 1 is silently ignored.
func reportStyles() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		// 164 is the first index available for a custom format; 0-163 are
		// reserved by the spec for the built-ins.
		`<numFmts count="1"><numFmt numFmtId="164" formatCode="#,##0.00"/></numFmts>` +
		`<fonts count="5">` +
		`<font><sz val="11"/><color rgb="FF` + inkHex + `"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="11"/><color rgb="FF` + inkHex + `"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="15"/><color rgb="FF` + brandHex + `"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="12"/><color rgb="FF` + inkHex + `"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="11"/><color rgb="FFFFFFFF"/><name val="Calibri"/></font>` +
		`</fonts>` +
		`<fills count="3">` +
		`<fill><patternFill patternType="none"/></fill>` +
		`<fill><patternFill patternType="gray125"/></fill>` +
		`<fill><patternFill patternType="solid"><fgColor rgb="FF` + brandHex + `"/>` +
		`<bgColor indexed="64"/></patternFill></fill>` +
		`<fill><patternFill patternType="solid"><fgColor rgb="FF` + bandHex + `"/>` +
		`<bgColor indexed="64"/></patternFill></fill>` +
		`</fills>` +
		`<borders count="2"><border/>` +
		`<border>` +
		`<left style="thin"><color rgb="FF` + ruleHex + `"/></left>` +
		`<right style="thin"><color rgb="FF` + ruleHex + `"/></right>` +
		`<top style="thin"><color rgb="FF` + ruleHex + `"/></top>` +
		`<bottom style="thin"><color rgb="FF` + ruleHex + `"/></bottom>` +
		`</border></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		// Order here defines the Style constants. Do not reorder.
		`<cellXfs count="11">` +
		// 0 normal
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		// 1 title
		`<xf numFmtId="0" fontId="2" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		// 2 section heading
		`<xf numFmtId="0" fontId="3" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		// 3 table header — white on brand, centred, so a long table keeps an
		// obvious top even when the reader has scrolled past it
		`<xf numFmtId="0" fontId="4" fillId="2" borderId="1" xfId="0" applyFont="1" ` +
		`applyFill="1" applyBorder="1" applyAlignment="1">` +
		`<alignment horizontal="center" vertical="center" wrapText="1"/></xf>` +
		// 4 table text
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyBorder="1" ` +
		`applyAlignment="1"><alignment vertical="center"/></xf>` +
		// 5 table integer — thousands separated, right aligned so digits line up
		`<xf numFmtId="3" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" ` +
		`applyBorder="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf>` +
		// 6 table decimal
		`<xf numFmtId="164" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" ` +
		`applyBorder="1" applyAlignment="1"><alignment horizontal="right" vertical="center"/></xf>` +
		// 7 strong prose
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		// 8-10 banded variants of the table styles. Alternating fill is what
		// keeps the eye on one row across a wide table; without it a
		// twenty-row breakdown is where misreadings happen.
		`<xf numFmtId="0" fontId="0" fillId="3" borderId="1" xfId="0" applyFill="1" applyBorder="1" ` +
		`applyAlignment="1"><alignment vertical="center"/></xf>` +
		`<xf numFmtId="3" fontId="0" fillId="3" borderId="1" xfId="0" applyNumberFormat="1" ` +
		`applyFill="1" applyBorder="1" applyAlignment="1">` +
		`<alignment horizontal="right" vertical="center"/></xf>` +
		`<xf numFmtId="164" fontId="0" fillId="3" borderId="1" xfId="0" applyNumberFormat="1" ` +
		`applyFill="1" applyBorder="1" applyAlignment="1">` +
		`<alignment horizontal="right" vertical="center"/></xf>` +
		`</cellXfs>` +
		`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
		`</styleSheet>`
}

// sheetXML renders one worksheet. Uses inline strings rather than a shared
// string table: the table saves space only when values repeat, which a report
// sheet's labels barely do, and it adds a second part that must stay in sync.
func sheetXML(sheet Sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		// Gridlines off: the tables carry their own borders, and the default
		// grid makes the prose between them look like empty spreadsheet rather
		// than part of a document.
		`<sheetViews><sheetView showGridLines="0" workbookViewId="0">`)
	if sheet.FreezeHeaderRow > 0 {
		// ySplit counts the rows ABOVE the split, so freezing "through row N"
		// means a split of N with the pane starting at N+1.
		b.WriteString(fmt.Sprintf(
			`<pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/>`+
				`<selection pane="bottomLeft"/>`,
			sheet.FreezeHeaderRow, sheet.FreezeHeaderRow+1))
	}
	b.WriteString(`</sheetView></sheetViews>`)
	if len(sheet.ColWidths) > 0 {
		// <cols> must precede <sheetData>; Excel rejects the sheet otherwise.
		b.WriteString(`<cols>`)
		for i, w := range sheet.ColWidths {
			b.WriteString(fmt.Sprintf(`<col min="%d" max="%d" width="%s" customWidth="1"/>`,
				i+1, i+1, strconv.FormatFloat(w, 'f', 2, 64)))
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	for r, row := range sheet.Rows {
		// Header rows get extra height so the centred white label has room to
		// breathe; everything else uses the sheet default.
		attrs := ""
		if len(row) > 0 && row[0].Style == StyleTableHeader {
			attrs = ` ht="22" customHeight="1"`
		}
		b.WriteString(fmt.Sprintf(`<row r="%d"%s>`, r+1, attrs))
		for c, cell := range row {
			ref := columnName(c) + strconv.Itoa(r+1)
			style := ""
			if cell.Style != StyleNormal {
				style = fmt.Sprintf(` s="%d"`, int(cell.Style))
			}
			if cell.Numeric {
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`,
					ref, style, strconv.FormatFloat(cell.Number, 'f', -1, 64)))
				continue
			}
			if cell.Text == "" {
				// A styled empty cell still needs an element, or a table row
				// with a blank field loses its border and the grid breaks.
				if cell.Style == StyleNormal {
					continue
				}
				b.WriteString(fmt.Sprintf(`<c r="%s"%s/>`, ref, style))
				continue
			}
			b.WriteString(fmt.Sprintf(`<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
				ref, style, escape(cell.Text)))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData>`)
	if sheet.AutoFilterRange != "" {
		// autoFilter must follow sheetData; Excel rejects the sheet otherwise.
		b.WriteString(fmt.Sprintf(`<autoFilter ref="%s"/>`, sheet.AutoFilterRange))
	}
	b.WriteString(`</worksheet>`)
	return b.String()
}
