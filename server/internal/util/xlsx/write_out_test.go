package xlsx

import (
	"os"
	"testing"
)

// TestWriteSampleFile emits a workbook to disk when XLSX_OUT is set, so the
// file can be opened by a real spreadsheet application. Skipped otherwise —
// a unit test must not litter the working tree.
func TestWriteSampleFile(t *testing.T) {
	path := os.Getenv("XLSX_OUT")
	if path == "" {
		t.Skip("set XLSX_OUT to emit a sample workbook")
	}
	data, err := Write([]Sheet{{Name: "Hisobot", Rows: [][]Cell{
		{HeaderCell("Backlog 41 taga o'sdi — nisbat 6:1 & <test>")},
		nil,
		{HeaderCell("Oy"), HeaderCell("Yaratildi")},
		{AutoCell("Yanvar"), AutoCell("360")},
		{AutoCell("Fevral"), AutoCell("2 417")},
		{AutoCell("Mart"), AutoCell("—")},
	}}})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("save: %v", err)
	}
}
