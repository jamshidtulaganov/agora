package handler

import (
	"strconv"
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
//
// title becomes the document heading in the first row. The sheet TAB is capped
// at 31 characters by Excel, so a report called "Weekly Bitrix report — backlog
// growth & load" would arrive truncated to nonsense; carrying it as a cell
// keeps it readable.
func markdownToSheet(name, title, body string) xlsx.Sheet {
	sheet := xlsx.Sheet{Name: name}
	if strings.TrimSpace(title) != "" {
		sheet.Rows = append(sheet.Rows,
			[]xlsx.Cell{xlsx.TextCell(title).With(xlsx.StyleTitle)},
			nil,
		)
	}
	lines := strings.Split(body, "\n")
	// The report's first prose line carries the finding, whether or not a title
	// row precedes it — the title names the document, the headline states what
	// it found.
	headline := true

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		switch {
		case trimmed == "":
			// One spacer, never a run of them. Markdown carries blank lines
			// freely and section splitting adds more; a sheet with four empty
			// rows between blocks reads as a broken export.
			if n := len(sheet.Rows); n == 0 || len(sheet.Rows[n-1]) > 0 {
				sheet.Rows = append(sheet.Rows, nil)
			}

		case strings.HasPrefix(trimmed, "#"):
			// Headings keep the report's sections; without them a long report
			// is one undifferentiated run of rows.
			sheet.Rows = append(sheet.Rows, []xlsx.Cell{
				xlsx.TextCell(strings.TrimSpace(strings.TrimLeft(trimmed, "# "))).
					With(xlsx.StyleSection),
			})

		case strings.Trim(trimmed, "-") == "" && strings.HasPrefix(trimmed, "---"):
			if n := len(sheet.Rows); n == 0 || len(sheet.Rows[n-1]) > 0 {
				sheet.Rows = append(sheet.Rows, nil) // a rule is just a break
			}

		case strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableDivider(lines[i+1]):
			// Header row, then the divider is skipped, then the body rows.
			header := splitTableRow(trimmed)
			headerCells := make([]xlsx.Cell, 0, len(header))
			for _, h := range header {
				headerCells = append(headerCells, xlsx.HeaderCell(stripInlineMarkdown(h)))
			}
			sheet.Rows = append(sheet.Rows, headerCells)
			width := len(headerCells)

			i += 2
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				cells := splitTableRow(strings.TrimSpace(lines[i]))
				row := make([]xlsx.Cell, 0, width)
				for _, c := range cells {
					// AutoCell is what makes the file worth sending: a count
					// stored as text cannot be summed or sorted, which is the
					// only reason to ship a spreadsheet instead of a page.
					// TableCell then borders it and picks the number format.
					row = append(row, xlsx.TableCell(xlsx.AutoCell(stripInlineMarkdown(c))))
				}
				// Pad a short row to the header's width. A ragged markdown row
				// would otherwise leave the table's right edge open on that
				// line, which reads as a rendering fault.
				for len(row) < width {
					row = append(row, xlsx.TextCell("").With(xlsx.StyleTableText))
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
			// The report's first prose line is its finding, and it is what the
			// Telegram caption shows. Giving it weight in the file too keeps
			// the two views of the report consistent.
			cell := xlsx.TextCell(stripInlineMarkdown(trimmed))
			if headline {
				cell = cell.With(xlsx.StyleStrong)
				headline = false
			}
			sheet.Rows = append(sheet.Rows, []xlsx.Cell{cell})
		}
	}
	// A report body almost always ends in a newline, which would otherwise
	// leave a trailing empty row under every sheet.
	for len(sheet.Rows) > 0 && len(sheet.Rows[len(sheet.Rows)-1]) == 0 {
		sheet.Rows = sheet.Rows[:len(sheet.Rows)-1]
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
//
// One sheet per section that contains a table, plus a summary sheet carrying
// the title, the finding and the prose. That is the shape a reader expects
// from a report workbook: the overview opens first, and each breakdown is its
// own tab they can sort and filter without disturbing anything else.
//
// A report with no tables stays a single sheet — splitting prose across tabs
// would be structure for its own sake.
func renderReportXLSX(title, body string) ([]byte, error) {
	sections := splitReportSections(body)
	tabled := 0
	for _, sec := range sections {
		if sec.hasTable {
			tabled++
		}
	}
	if tabled < 2 {
		return xlsx.Write([]xlsx.Sheet{markdownToSheet(sheetTabName(title), title, body)})
	}

	sheets := make([]xlsx.Sheet, 0, tabled+1)
	// The summary keeps every section's prose, so nothing is lost by the split
	// — a reader who never opens a data tab still gets the whole narrative.
	summary := markdownToSheet(sheetTabName(title), title, summaryBody(sections))
	sheets = append(sheets, summary)

	used := map[string]bool{sanitizedLower(summary.Name): true}
	for _, sec := range sections {
		if !sec.hasTable {
			continue
		}
		sheet := markdownToSheet(uniqueSheetName(sec.heading, used), "", sec.body)
		applyTableAffordances(&sheet)
		sheets = append(sheets, sheet)
	}
	return xlsx.Write(sheets)
}

// reportSection is one `##` block of the report.
type reportSection struct {
	heading  string
	body     string
	hasTable bool
}

// splitReportSections cuts the report at its headings. Text before the first
// heading is the lead — the finding — and is kept as an unnamed section so it
// still opens the summary.
func splitReportSections(body string) []reportSection {
	lines := strings.Split(body, "\n")
	sections := []reportSection{{}}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "#") {
			sections = append(sections, reportSection{
				heading: strings.TrimSpace(strings.TrimLeft(trimmed, "# ")),
			})
			continue
		}
		cur := &sections[len(sections)-1]
		if strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && isTableDivider(lines[i+1]) {
			cur.hasTable = true
		}
		cur.body += lines[i] + "\n"
	}
	return sections
}

// summaryBody rebuilds the prose-only report: every section's heading survives,
// and a section whose content moved to its own tab is replaced by a pointer to
// it rather than silently vanishing.
func summaryBody(sections []reportSection) string {
	var b strings.Builder
	for _, sec := range sections {
		if sec.heading != "" {
			b.WriteString("## " + sec.heading + "\n\n")
		}
		if sec.hasTable {
			b.WriteString("Alohida varaqda: " + sec.heading + "\n\n")
			// Prose that sat alongside the table is kept — it usually explains
			// the number, and the number without it is a bare column.
			b.WriteString(proseOnly(sec.body))
			continue
		}
		b.WriteString(sec.body)
	}
	return b.String()
}

// proseOnly drops table rows, keeping the sentences around them.
func proseOnly(body string) string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// applyTableAffordances turns a one-table sheet into something interrogable:
// the header stays put while scrolling, and the filter lets a reader sort by
// count or narrow to one person without editing anything.
func applyTableAffordances(sheet *xlsx.Sheet) {
	headerRow, lastRow, width := 0, 0, 0
	for i, row := range sheet.Rows {
		if len(row) < 2 {
			continue
		}
		if headerRow == 0 && row[0].Style == xlsx.StyleTableHeader {
			headerRow, width = i+1, len(row)
		}
		if headerRow > 0 && i+1 > lastRow {
			lastRow = i + 1
		}
	}
	if headerRow == 0 || width == 0 {
		return
	}
	sheet.FreezeHeaderRow = headerRow
	sheet.AutoFilterRange = "A" + strconv.Itoa(headerRow) + ":" +
		xlsx.ColumnName(width-1) + strconv.Itoa(lastRow)

	// Band every other data row. Applied here rather than during conversion
	// because only now is it known which rows belong to one table.
	for i := headerRow; i < len(sheet.Rows); i++ {
		if (i-headerRow)%2 == 1 {
			for c := range sheet.Rows[i] {
				sheet.Rows[i][c].Style = sheet.Rows[i][c].Style.Banded()
			}
		}
	}
}

// uniqueSheetName keeps Excel from rejecting a workbook over duplicate tabs,
// which two sections with the same heading would otherwise produce.
func uniqueSheetName(name string, used map[string]bool) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = "Sheet"
	}
	candidate := base
	for n := 2; used[sanitizedLower(candidate)]; n++ {
		candidate = base + " " + strconv.Itoa(n)
	}
	used[sanitizedLower(candidate)] = true
	return candidate
}

// sanitizedLower is the identity Excel compares tab names by: it treats them
// case-insensitively, so "Oylar" and "oylar" collide.
func sanitizedLower(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// sheetTabName is the short label on the tab. The full title lives in the first
// row, so this only has to be recognisable, not complete.
func sheetTabName(title string) string {
	if dash := strings.Index(title, " — "); dash > 0 {
		return strings.TrimSpace(title[:dash])
	}
	return strings.TrimSpace(title)
}
