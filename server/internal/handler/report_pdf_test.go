package handler

import (
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

func TestRenderReportPDFProducesADocument(t *testing.T) {
	data, err := renderReportPDF("Hisobot", "Xulosa.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// %PDF is the magic; anything else opens as corrupt in Telegram's viewer.
	if !bytes.HasPrefix(data, []byte("%PDF")) {
		t.Fatalf("output is not a PDF (%d bytes)", len(data))
	}
}

func TestPDFCarriesCyrillic(t *testing.T) {
	// The reason a font is embedded at all: PDF's built-in fonts are Latin-1,
	// so "Стадия" and "Сделаны" would render as mojibake. A silent success
	// here would ship an unreadable report.
	data, err := renderReportPDF("Стадия bo'yicha", "Сделаны 1902\n")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(data) < 2000 {
		t.Fatalf("suspiciously small PDF (%d bytes) — the font may not be embedded", len(data))
	}
}

func TestLooksNumericDrivesAlignment(t *testing.T) {
	// Loose on purpose: "9.1%" and "2 417" are numbers to a reader even if not
	// to a parser, and a column of digits only compares at a glance when it is
	// right-aligned.
	for _, in := range []string{"360", "2 417", "9.1", "9.1%", "-12", "1,204"} {
		if !looksNumeric(in) {
			t.Errorf("%q should right-align", in)
		}
	}
	for _, in := range []string{"", "Yanvar", "Code Review", "—", "n/a"} {
		if looksNumeric(in) {
			t.Errorf("%q should stay left-aligned", in)
		}
	}
}

func TestPDFTableWidthsFillThePage(t *testing.T) {
	// A table floating in the left third looks like a rendering fault; one
	// running off the edge loses its last column.
	pdf := newTestPDF(t)
	header := []string{"Xodim", "Ochiq"}
	rows := [][]string{{"Saidazim Saidnabiyev", "47"}, {"A", "1"}}
	widths := pdfColumnWidths(pdf, header, rows, 178)
	total := 0.0
	for _, w := range widths {
		total += w
	}
	if total < 177 || total > 179 {
		t.Fatalf("columns total %.1fmm, want the usable width", total)
	}
	// The name column must be the wide one — equal columns would wrap every
	// name while leaving the counts swimming.
	if widths[0] <= widths[1] {
		t.Fatalf("name column %.1f is not wider than the count column %.1f", widths[0], widths[1])
	}
}

func TestPDFStripsMarkdownMarkers(t *testing.T) {
	// A PDF renders no markdown; leftover asterisks read as a bug.
	if got := stripInlineMarkdown("**Alisher** bilan `oqim`"); strings.ContainsAny(got, "*`") {
		t.Fatalf("markers survived: %q", got)
	}
}

// newTestPDF builds a document with the fonts registered, which the width
// measurement needs.
func newTestPDF(t *testing.T) *fpdf.Fpdf {
	t.Helper()
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddUTF8FontFromBytes("Go", "", goregular.TTF)
	pdf.AddUTF8FontFromBytes("Go", "B", gobold.TTF)
	pdf.AddPage()
	return pdf
}
