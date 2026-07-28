package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/util/xlsx"
)

func TestMarkdownToSheetKeepsTableCellsSeparate(t *testing.T) {
	// The failure this guards: dumping each markdown line into one cell would
	// produce a file with the .xlsx extension and none of the benefit.
	body := "Backlog o'sdi.\n\n## Oylar\n\n| Oy | Yaratildi |\n|---|---|\n| Yanvar | 360 |\n| Fevral | 2 417 |\n"
	sheet := markdownToSheet("Hisobot", body)

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
	if header == nil || !header[0].Header || !header[1].Header {
		t.Fatal("the table header row must be present and bold")
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
	sheet := markdownToSheet("S", "**Backlog 41 taga o'sdi** — nisbat `6:1`.")
	if got := sheet.Rows[0][0].Text; got != "Backlog 41 taga o'sdi — nisbat 6:1." {
		t.Fatalf("got %q", got)
	}
}

func TestMarkdownToSheetKeepsHeadingsAndBullets(t *testing.T) {
	// Prose is kept, not discarded: a sheet of bare numbers loses the finding
	// that made them worth sending.
	sheet := markdownToSheet("S", "# Xulosa\n\n- Birinchi band\n")
	if !sheet.Rows[0][0].Header || sheet.Rows[0][0].Text != "Xulosa" {
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
