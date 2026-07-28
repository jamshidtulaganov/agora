package xlsx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestAutoCellKeepsNumbersNumeric(t *testing.T) {
	// This is the entire reason to ship a spreadsheet instead of a page: a
	// count stored as text cannot be summed, sorted or charted.
	for _, in := range []string{"360", "2 417", "2,417", " 41 ", "-12", "3.5"} {
		if c := AutoCell(in); !c.Numeric {
			t.Errorf("%q was stored as text", in)
		}
	}
	if got := AutoCell("2 417"); got.Number != 2417 {
		t.Errorf("thousands separator not stripped: got %v", got.Number)
	}
}

func TestAutoCellKeepsPlaceholdersAsText(t *testing.T) {
	// A lone dash means "no value", not a negative number. Storing it as one
	// would put a phantom −0 into a column someone is about to sum.
	for _, in := range []string{"—", "-", "Yanvar", "6:1", "n/a"} {
		if c := AutoCell(in); c.Numeric {
			t.Errorf("%q was stored as a number", in)
		}
	}
}

func TestColumnNameCrossesZ(t *testing.T) {
	// A by-month table plus totals already runs into the teens; a wrong letter
	// past Z corrupts the sheet silently rather than failing.
	cases := map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"}
	for i, want := range cases {
		if got := columnName(i); got != want {
			t.Errorf("columnName(%d) = %s, want %s", i, got, want)
		}
	}
}

func TestSanitizeSheetName(t *testing.T) {
	// Excel refuses to open the whole workbook on a bad sheet name, so this is
	// corrected rather than reported.
	if got := sanitizeSheetName("Hisobot: 2026/07 [yangi]"); strings.ContainsAny(got, `:\/?*[]`) {
		t.Errorf("forbidden character survived: %q", got)
	}
	long := sanitizeSheetName(strings.Repeat("о", 60))
	if n := len([]rune(long)); n != 31 {
		t.Errorf("got %d runes, want the 31-character cap", n)
	}
	if sanitizeSheetName("   ") == "" {
		t.Error("an empty name must fall back, not produce an unnamed sheet")
	}
}

func TestWriteProducesEveryRequiredPart(t *testing.T) {
	data, err := Write([]Sheet{{Name: "Hisobot", Rows: [][]Cell{
		{HeaderCell("Oy"), HeaderCell("Soni")},
		{TextCell("Yanvar"), NumberCell(360)},
	}}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("not a readable zip: %v", err)
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		seen[f.Name] = true
	}
	// Every one of these is mandatory; a missing part makes the file open as
	// "corrupt" with no indication of which piece is absent.
	for _, required := range []string{
		"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml",
		"xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml",
	} {
		if !seen[required] {
			t.Errorf("missing part %s", required)
		}
	}
}

func TestWriteEscapesCellText(t *testing.T) {
	// Cell text is agent-authored. An unescaped & or < produces a file the
	// reader refuses to open — a silent failure at the last step of a report.
	data, err := Write([]Sheet{{Name: "S", Rows: [][]Cell{{TextCell(`a & b <c> "d"`)}}}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		if f.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		rc, _ := f.Open()
		buf := new(bytes.Buffer)
		buf.ReadFrom(rc)
		rc.Close()
		body := buf.String()
		if strings.Contains(body, "a & b") || strings.Contains(body, "<c>") {
			t.Fatalf("cell text was not escaped: %s", body)
		}
		if !strings.Contains(body, "&amp;") {
			t.Fatalf("expected an escaped ampersand, got: %s", body)
		}
	}
}

func TestWriteHandlesNoSheets(t *testing.T) {
	// An empty report must still produce an openable file rather than a
	// zero-byte attachment nobody can diagnose.
	data, err := Write(nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("empty workbook is not readable: %v", err)
	}
}
