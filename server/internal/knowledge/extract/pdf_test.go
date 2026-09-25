package extract

import (
	"bytes"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
)

func requirePDFToText(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext (poppler) is not installed")
	}
}

// buildPDF renders one PDF page per element of pages; each page is a list of
// lines, and an empty string makes a blank line (paragraph break).
func buildPDF(t *testing.T, pages [][]string) []byte {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetFont("Helvetica", "", 11)
	for _, lines := range pages {
		pdf.AddPage()
		for _, l := range lines {
			pdf.CellFormat(0, 6, l, "", 1, "L", false, 0, "")
		}
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractPDF(t *testing.T) {
	requirePDFToText(t)
	data := buildPDF(t, [][]string{
		{
			"1. Scope",
			"",
			"This policy covers every account placed with the collections team, including",
			"accounts transferred from partner agencies during the quarter.",
			"",
			"- Call the customer first",
			"- Send the written notice",
			"",
			"Page 1",
		},
		{
			"Approval Process",
			"",
			"Write-offs above the threshold need approval from the department head and a",
			"second reviewer before the account is closed in the billing system.",
			"",
			"Page 2",
		},
	})
	doc := mustExtract(t, "Collections Policy.pdf", "application/pdf", data)
	if doc.PageCount != 2 || doc.Scanned {
		t.Fatalf("PageCount=%d Scanned=%v", doc.PageCount, doc.Scanned)
	}
	if doc.Title != "Collections Policy" {
		t.Errorf("title = %q", doc.Title)
	}
	want := []string{
		"h1:1. Scope @p. 1",
		"paragraph:This policy covers every account placed with the collections team, including\naccounts transferred from partner agencies during the quarter. @p. 1",
		"list:- Call the customer first\n- Send the written notice @p. 1",
		"h2:Approval Process @p. 2",
		"paragraph:Write-offs above the threshold need approval from the department head and a\nsecond reviewer before the account is closed in the billing system. @p. 2",
	}
	if got := blockSummary(doc.Blocks); !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}
}

func TestExtractPDFScanned(t *testing.T) {
	requirePDFToText(t)
	doc := mustExtract(t, "scan.pdf", "", buildPDF(t, [][]string{{}, {}, {"7"}}))
	if !doc.Scanned || doc.PageCount != 3 {
		t.Fatalf("Scanned=%v PageCount=%d, want true/3", doc.Scanned, doc.PageCount)
	}
	if len(doc.Blocks) != 0 {
		t.Errorf("blocks = %q, want none (page number stripped)", blockSummary(doc.Blocks))
	}
}

func TestExtractPDFDamaged(t *testing.T) {
	requirePDFToText(t)
	_, err := Extract(t.Context(), "broken.pdf", "", []byte("%PDF-1.4 garbage"))
	if err == nil || !strings.Contains(err.Error(), "could not read the PDF") {
		t.Fatalf("err = %v", err)
	}
}

func TestPDFLayoutHelpers(t *testing.T) {
	t.Run("running headers and page numbers", func(t *testing.T) {
		pages := [][]string{
			{"ACME Corp — Confidential", "Body one", "Page 1 of 3"},
			{"ACME Corp — Confidential", "Body two", "Page 2 of 3"},
			{"ACME Corp — Confidential", "Body three", "- 3 -"},
		}
		stripRunningLines(pages)
		for i, p := range pages {
			if p[0] != "" || p[2] != "" || !strings.HasPrefix(p[1], "Body") {
				t.Errorf("page %d = %q", i+1, p)
			}
		}
	})
	t.Run("tabular paragraphs keep alignment, prose is joined", func(t *testing.T) {
		segs := segmentPlain("  Bucket     Rate     Owner\n  0-30       5%       Ann\n\nThe  fee  is  charged  on  manage-\nment accounts.", "p. 1", true)
		want := []plainSeg{
			{kind: KindParagraph, text: "Bucket     Rate     Owner\n0-30       5%       Ann", loc: "p. 1"},
			{kind: KindParagraph, text: "The fee is charged on management accounts.", loc: "p. 1"},
		}
		if !reflect.DeepEqual(segs, want) {
			t.Errorf("segs = %+v", segs)
		}
	})
}
