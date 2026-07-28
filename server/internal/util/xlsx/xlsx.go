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
	"strconv"
	"strings"
)

// Cell is one value. Number wins when Numeric is true; otherwise Text is used.
type Cell struct {
	Text    string
	Number  float64
	Numeric bool
	// Header marks a cell for the bold style. Applied per cell rather than per
	// row because a report block's first row is a header while a prose line
	// above it is not.
	Header bool
}

// Text returns a text cell.
func TextCell(s string) Cell { return Cell{Text: s} }

// HeaderCell returns a bold text cell.
func HeaderCell(s string) Cell { return Cell{Text: s, Header: true} }

// NumberCell returns a numeric cell.
func NumberCell(f float64) Cell { return Cell{Number: f, Numeric: true} }

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
}

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

	// The styles part carries exactly two cell formats: plain (index 0) and
	// bold (index 1). Excel requires the full font/fill/border/cellStyleXfs
	// scaffolding even when almost all of it is empty.
	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<fonts count="2"><font><sz val="11"/><name val="Calibri"/></font>` +
		`<font><b/><sz val="11"/><name val="Calibri"/></font></fonts>` +
		`<fills count="1"><fill><patternFill patternType="none"/></fill></fills>` +
		`<borders count="1"><border/></borders>` +
		`<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>` +
		`<cellXfs count="2">` +
		`<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>` +
		`<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>` +
		`</cellXfs>` +
		// cellStyles is not optional in practice: without the Normal style,
		// readers warn about a missing default and the pickier ones substitute
		// their own, which loses the bold header.
		`<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>` +
		`</styleSheet>`

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

// sheetXML renders one worksheet. Uses inline strings rather than a shared
// string table: the table saves space only when values repeat, which a report
// sheet's labels barely do, and it adds a second part that must stay in sync.
func sheetXML(sheet Sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range sheet.Rows {
		b.WriteString(fmt.Sprintf(`<row r="%d">`, r+1))
		for c, cell := range row {
			ref := columnName(c) + strconv.Itoa(r+1)
			style := ""
			if cell.Header {
				style = ` s="1"`
			}
			if cell.Numeric {
				b.WriteString(fmt.Sprintf(`<c r="%s"%s><v>%s</v></c>`,
					ref, style, strconv.FormatFloat(cell.Number, 'f', -1, 64)))
				continue
			}
			if cell.Text == "" {
				continue // an empty cell needs no element at all
			}
			b.WriteString(fmt.Sprintf(`<c r="%s"%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`,
				ref, style, escape(cell.Text)))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}
