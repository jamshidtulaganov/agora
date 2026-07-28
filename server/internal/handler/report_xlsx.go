package handler

import (
	"strings"

	"github.com/multica-ai/multica/server/internal/util/xlsx"
)

// Rendering an agent's markdown report into a spreadsheet.
//
// The HTML page made the numbers READABLE. A spreadsheet makes them USABLE:
// the reader can sort a column, sum it, filter it, or paste it into a chart —
// which is what happens to a per-assignee or per-month breakdown the moment
// someone wants to act on it.
//
// So the conversion's whole job is to keep cells as cells. A markdown table
// becomes a real grid with one value per cell, and anything that parses as a
// number is written as a number. Dumping the report as one text blob per row
// would produce a file with the .xlsx extension and none of the benefit.
//
// Prose is kept, not discarded: the headline and the single recommendation are
// the parts a manager actually reads, and a sheet of bare numbers loses the
// finding that made them worth sending.

// markdownToSheet converts a report body into one worksheet.
func markdownToSheet(name, body string) xlsx.Sheet {
	sheet := xlsx.Sheet{Name: name}
	lines := strings.Split(body, "\n")

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		switch {
		case trimmed == "":
			sheet.Rows = append(sheet.Rows, nil) // a blank spacer row

		case strings.HasPrefix(trimmed, "#"):
			// Headings become bold single cells so the sheet keeps the report's
			// sections; without them a long report is one undifferentiated run
			// of rows.
			sheet.Rows = append(sheet.Rows, []xlsx.Cell{
				xlsx.HeaderCell(strings.TrimSpace(strings.TrimLeft(trimmed, "# "))),
			})

		case strings.Trim(trimmed, "-") == "" && strings.HasPrefix(trimmed, "---"):
			sheet.Rows = append(sheet.Rows, nil) // a rule is just a break here

		case strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableDivider(lines[i+1]):
			// Header row, then the divider is skipped, then the body rows.
			header := splitTableRow(trimmed)
			headerCells := make([]xlsx.Cell, 0, len(header))
			for _, h := range header {
				headerCells = append(headerCells, xlsx.HeaderCell(stripInlineMarkdown(h)))
			}
			sheet.Rows = append(sheet.Rows, headerCells)

			i += 2
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				cells := splitTableRow(strings.TrimSpace(lines[i]))
				row := make([]xlsx.Cell, 0, len(cells))
				for _, c := range cells {
					// AutoCell is what makes the file worth sending: a count
					// stored as text cannot be summed or sorted, which is the
					// only reason to ship a spreadsheet instead of a page.
					row = append(row, xlsx.AutoCell(stripInlineMarkdown(c)))
				}
				sheet.Rows = append(sheet.Rows, row)
				i++
			}
			i-- // the outer loop advances

		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			sheet.Rows = append(sheet.Rows, []xlsx.Cell{
				xlsx.TextCell("• " + stripInlineMarkdown(trimmed[2:])),
			})

		default:
			sheet.Rows = append(sheet.Rows, []xlsx.Cell{xlsx.TextCell(stripInlineMarkdown(trimmed))})
		}
	}
	// Derived last, from the finished rows: a label like "Median yopilish
	// (kun)" clipped to "Median yo" makes the number beside it meaningless.
	sheet.ColWidths = xlsx.AutoWidths(sheet.Rows)
	return sheet
}

// stripInlineMarkdown removes the emphasis markers a spreadsheet cannot render.
// Left in place they show up as literal asterisks and backticks in the cell,
// which reads as a formatting bug rather than emphasis.
func stripInlineMarkdown(s string) string {
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "`", "")
	return strings.TrimSpace(s)
}

// renderReportXLSX builds the workbook for a report body.
func renderReportXLSX(title, body string) ([]byte, error) {
	return xlsx.Write([]xlsx.Sheet{markdownToSheet(title, body)})
}
