package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/util/xlsx"
)

func TestMarkdownToSheetKeepsTableCellsSeparate(t *testing.T) {
	// The failure this guards: dumping each markdown line into one cell would
	// produce a file with the .xlsx extension and none of the benefit.
	body := "Backlog o'sdi.\n\n## Oylar\n\n| Oy | Yaratildi |\n|---|---|\n| Yanvar | 360 |\n| Fevral | 2 417 |\n"
	sheet := markdownToSheet("Hisobot", "", body)

	var header, first []xlsx.Cell
	for _, row := range sheet.Rows {
		if len(row) == 2 && row[0].Text == "Oy" {
			header = row
			continue
		}
		if len(row) == 2 && row[0].Text == "Yanvar" {
			first = row
		}
	}
	if header == nil || header[0].Style != xlsx.StyleTableHeader || header[1].Style != xlsx.StyleTableHeader {
		t.Fatal("the table header row must be present and styled as a header")
	}
	if first == nil {
		t.Fatal("the first data row is missing")
	}
	if !first[1].Numeric || first[1].Number != 360 {
		t.Fatalf("count was not stored as a number: %+v", first[1])
	}
}

func TestMarkdownToSheetStripsEmphasis(t *testing.T) {
	// A spreadsheet renders no markdown; leftover asterisks read as a
	// formatting bug rather than emphasis.
	sheet := markdownToSheet("S", "", "**Backlog 41 taga o'sdi** — nisbat `6:1`.")
	if got := sheet.Rows[0][0].Text; got != "Backlog 41 taga o'sdi — nisbat 6:1." {
		t.Fatalf("got %q", got)
	}
}

func TestMarkdownToSheetKeepsHeadingsAndBullets(t *testing.T) {
	// Prose is kept, not discarded: a sheet of bare numbers loses the finding
	// that made them worth sending.
	sheet := markdownToSheet("S", "", "# Xulosa\n\n- Birinchi band\n")
	if sheet.Rows[0][0].Style != xlsx.StyleSection || sheet.Rows[0][0].Text != "Xulosa" {
		t.Fatalf("heading lost: %+v", sheet.Rows[0])
	}
	found := false
	for _, row := range sheet.Rows {
		if len(row) == 1 && row[0].Text == "• Birinchi band" {
			found = true
		}
	}
	if !found {
		t.Fatal("bullet lost")
	}
}

func TestRenderReportXLSXProducesAWorkbook(t *testing.T) {
	data, err := renderReportXLSX("Hisobot", "| a | b |\n|---|---|\n| 1 | 2 |\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// PK is the zip magic; an .xlsx that is not a zip opens as corrupt.
	if len(data) < 4 || data[0] != 'P' || data[1] != 'K' {
		t.Fatalf("output is not a zip archive (%d bytes)", len(data))
	}
}

func TestMarkdownToSheetSizesColumnsToTheTables(t *testing.T) {
	// The bug this guards: with no widths, Excel clips "Median yopilish (kun)"
	// to "Median yo" and the number beside it stops meaning anything.
	body := "| Ko'rsatkich | Qiymat |\n|---|---|\n| Median yopilish (kun) | 4.5 |\n"
	sheet := markdownToSheet("S", "", body)
	if len(sheet.ColWidths) < 2 {
		t.Fatalf("no column widths derived: %+v", sheet.ColWidths)
	}
	if sheet.ColWidths[0] < float64(len("Median yopilish (kun)")) {
		t.Fatalf("first column is narrower than its widest label: %v", sheet.ColWidths[0])
	}
}

func TestProseDoesNotStretchTheLabelColumn(t *testing.T) {
	// A one-cell row is prose. Measuring it would stretch column A to sentence
	// length and leave every table on the sheet with an absurd label column —
	// Excel already spills a long value into empty neighbours.
	long := "Iyun-iyulda yopilish keskin tushdi va bu holat sprintdan tashqari kelayotgan ishlar bilan bog'liq."
	sheet := markdownToSheet("S", "", "| Oy | Soni |\n|---|---|\n| May | 305 |\n\n"+long)
	if sheet.ColWidths[0] > 20 {
		t.Fatalf("prose stretched the label column to %v", sheet.ColWidths[0])
	}
}

func TestTitleLeadsTheSheetAndTabStaysShort(t *testing.T) {
	// Excel caps a tab at 31 characters, so a full report title would arrive
	// truncated to nonsense. It goes in the first row instead.
	const title = "Haftalik Bitrix hisoboti — 28.07.2026"
	sheet := markdownToSheet(sheetTabName(title), title, "Backlog o'sdi.")
	if sheet.Rows[0][0].Text != title || sheet.Rows[0][0].Style != xlsx.StyleTitle {
		t.Fatalf("title row wrong: %+v", sheet.Rows[0])
	}
	if got := sheetTabName(title); got != "Haftalik Bitrix hisoboti" {
		t.Fatalf("tab name %q still carries the date suffix", got)
	}
}

func TestTableRowsArePaddedToTheHeaderWidth(t *testing.T) {
	// A ragged markdown row would leave the table's right edge unbordered on
	// that line, which reads as a rendering fault rather than missing data.
	sheet := markdownToSheet("S", "", "| a | b | c |\n|---|---|---|\n| 1 | 2 |\n")
	for _, row := range sheet.Rows {
		if len(row) > 0 && row[0].Numeric && row[0].Number == 1 {
			if len(row) != 3 {
				t.Fatalf("short row was not padded: %d cells", len(row))
			}
			if row[2].Style != xlsx.StyleTableText {
				t.Fatalf("the padding cell is unstyled, so its border is missing")
			}
			return
		}
	}
	t.Fatal("data row not found")
}

func TestNumbersGetTheRightTableFormat(t *testing.T) {
	// A count rendered as "360.00" looks like a measurement. Whole numbers and
	// decimals take different formats.
	sheet := markdownToSheet("S", "", "| a | b |\n|---|---|\n| 360 | 4.5 |\n")
	for _, row := range sheet.Rows {
		if len(row) == 2 && row[0].Numeric && row[0].Number == 360 {
			if row[0].Style != xlsx.StyleTableInt {
				t.Errorf("whole number got style %v", row[0].Style)
			}
			if row[1].Style != xlsx.StyleTableDecimal {
				t.Errorf("decimal got style %v", row[1].Style)
			}
			return
		}
	}
	t.Fatal("numeric row not found")
}
