package handler

import (
	"bytes"
	"strings"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

// Rendering a report as a PDF.
//
// Chosen over the spreadsheet for delivery because Telegram previews a PDF
// inline: a reader sees the report in the chat instead of a file they must
// download, decide about, and open. That is the difference between a report
// being read on a phone and being ignored.
//
// The trade is real and worth stating: a PDF cannot be sorted or filtered. The
// spreadsheet renderer is kept — a caller wanting a workbook still has one.
//
// Fonts are the reason this is not trivial. PDF's built-in fonts are Latin-1,
// so "Стадия" and "Сделаны" would come out as mojibake. The Go fonts cover
// Latin, Greek and Cyrillic and arrive as an ordinary module, so nothing binary
// is vendored into the repository and nothing is fetched at build time.

// Page geometry, in millimetres. A4 with margins wide enough that a table
// reaching the edge still reads as a document rather than a printout.
const (
	pdfMarginX     = 16.0
	pdfMarginTop   = 18.0
	pdfLineHeight  = 5.6
	pdfTableHeight = 7.0
)

// renderReportPDF converts a report body into a PDF document.
func renderReportPDF(title, body string) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(pdfMarginX, pdfMarginTop, pdfMarginX)
	pdf.SetAutoPageBreak(true, 16)
	// Registered from memory: the module ships the bytes, so there is no font
	// directory to find at runtime and no file to miss in a container image.
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	pdf.AddPage()

	pdf.SetTextColor(0x25, 0x63, 0xEB) // brand blue, as in the workbook
	pdf.SetFont("Go", "B", 17)
	pdf.MultiCell(0, 8, title, "", "L", false)
	pdf.Ln(2)
	pdf.SetTextColor(0x1F, 0x29, 0x37)

	lines := strings.Split(body, "\n")
	headline := true
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		switch {
		case trimmed == "":
			pdf.Ln(2.5)

		case strings.HasPrefix(trimmed, "#"):
			pdf.Ln(2)
			pdf.SetFont("Go", "B", 12.5)
			pdf.MultiCell(0, 6.5, stripInlineMarkdown(strings.TrimLeft(trimmed, "# ")), "", "L", false)
			pdf.SetFont("Go", "", 10)
			pdf.Ln(1)

		case strings.Trim(trimmed, "-") == "" && strings.HasPrefix(trimmed, "---"):
			pdf.Ln(1)
			y := pdf.GetY()
			pdf.SetDrawColor(0xD1, 0xD5, 0xDB)
			pdf.Line(pdfMarginX, y, 210-pdfMarginX, y)
			pdf.Ln(3)

		case strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableDivider(lines[i+1]):
			header := splitTableRow(trimmed)
			i += 2
			rows := [][]string{}
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				rows = append(rows, splitTableRow(strings.TrimSpace(lines[i])))
				i++
			}
			i--
			drawPDFTable(pdf, header, rows)

		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			pdf.SetFont("Go", "", 10)
			pdf.MultiCell(0, pdfLineHeight, "•  "+stripInlineMarkdown(trimmed[2:]), "", "L", false)

		default:
			// The lead line carries the finding; giving it weight matches the
			// workbook and the Telegram caption, so all three read the same.
			if headline {
				pdf.SetFont("Go", "B", 11)
				headline = false
			} else {
				pdf.SetFont("Go", "", 10)
			}
			pdf.MultiCell(0, pdfLineHeight, stripInlineMarkdown(trimmed), "", "L", false)
		}
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawPDFTable lays out one markdown table.
//
// Columns are sized from their content rather than split evenly: a per-assignee
// table has one wide name column and two narrow counts, and equal columns would
// wrap every name while leaving the numbers swimming.
func drawPDFTable(pdf *fpdf.Fpdf, header []string, rows [][]string) {
	if len(header) == 0 {
		return
	}
	usable := 210.0 - 2*pdfMarginX
	widths := pdfColumnWidths(pdf, header, rows, usable)

	pdf.SetFont("Go", "B", 9.5)
	pdf.SetFillColor(0x25, 0x63, 0xEB)
	pdf.SetTextColor(255, 255, 255)
	for c, cell := range header {
		pdf.CellFormat(widths[c], pdfTableHeight, stripInlineMarkdown(cell), "1", 0, "C", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Go", "", 9.5)
	pdf.SetTextColor(0x1F, 0x29, 0x37)
	pdf.SetDrawColor(0xD1, 0xD5, 0xDB)
	for r, row := range rows {
		// Alternating fill, as in the workbook: on a wide table the eye loses
		// the row, and that is where misreadings happen.
		fill := r%2 == 1
		if fill {
			pdf.SetFillColor(0xF3, 0xF6, 0xFB)
		}
		for c := range widths {
			text := ""
			if c < len(row) {
				text = stripInlineMarkdown(row[c])
			}
			// Numbers right, labels left — digits that line up can be compared
			// at a glance.
			align := "L"
			if c > 0 && looksNumeric(text) {
				align = "R"
			}
			pdf.CellFormat(widths[c], pdfTableHeight, text, "1", 0, align, fill, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.Ln(2)
}

// pdfColumnWidths sizes columns to their widest cell, then scales to the page.
func pdfColumnWidths(pdf *fpdf.Fpdf, header []string, rows [][]string, usable float64) []float64 {
	widths := make([]float64, len(header))
	measure := func(text string, bold bool) float64 {
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Go", style, 9.5)
		return pdf.GetStringWidth(stripInlineMarkdown(text)) + 6
	}
	for c, cell := range header {
		widths[c] = measure(cell, true)
	}
	for _, row := range rows {
		for c := range widths {
			if c < len(row) {
				if w := measure(row[c], false); w > widths[c] {
					widths[c] = w
				}
			}
		}
	}
	total := 0.0
	for _, w := range widths {
		total += w
	}
	if total == 0 {
		return widths
	}
	// Scale to fill the page whether the natural width is under or over it: a
	// table floating in the left third looks like a rendering fault, and one
	// running off the edge loses its last column.
	scale := usable / total
	for c := range widths {
		widths[c] *= scale
	}
	return widths
}

// looksNumeric reports whether a cell should be right-aligned. Deliberately
// loose — "9.1%" and "2 417" are numbers to a reader even if not to a parser.
func looksNumeric(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	digits := 0
	for _, r := range trimmed {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' || r == ',' || r == '%' || r == ' ' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return digits > 0
}
