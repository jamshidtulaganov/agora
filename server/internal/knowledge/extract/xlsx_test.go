package extract

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func xlsxBytes(t *testing.T, f *excelize.File) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setCell(t *testing.T, f *excelize.File, sheet, ref string, v any) {
	t.Helper()
	if err := f.SetCellValue(sheet, ref, v); err != nil {
		t.Fatal(err)
	}
}

func TestExtractXLSX(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "Rates"); err != nil {
		t.Fatal(err)
	}
	// Row 1 is empty; the header is row 2, columns B-E (A stays empty).
	for i, h := range []string{"Bucket", "Rate", "Effective", "Owner"} {
		setCell(t, f, "Rates", fmt.Sprintf("%c2", 'B'+i), h)
	}
	for r := 3; r <= 102; r++ {
		setCell(t, f, "Rates", fmt.Sprintf("B%d", r), fmt.Sprintf("bucket-%d", r))
		setCell(t, f, "Rates", fmt.Sprintf("C%d", r), float64(r)/100)
	}
	setCell(t, f, "Rates", "E3", "Ann")
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: ptr("yyyy-mm-dd")})
	if err != nil {
		t.Fatal(err)
	}
	setCell(t, f, "Rates", "D3", time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC))
	if err := f.SetCellStyle("Rates", "D3", "D3", dateStyle); err != nil {
		t.Fatal(err)
	}
	pct, err := f.NewStyle(&excelize.Style{NumFmt: 9}) // "0%"
	if err != nil {
		t.Fatal(err)
	}
	if err := f.SetCellStyle("Rates", "C3", "C3", pct); err != nil {
		t.Fatal(err)
	}

	if _, err := f.NewSheet("Secret"); err != nil {
		t.Fatal(err)
	}
	setCell(t, f, "Secret", "A1", "hidden header")
	setCell(t, f, "Secret", "A2", "hidden value")
	if err := f.SetSheetVisible("Secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.NewSheet("Empty"); err != nil {
		t.Fatal(err)
	}

	doc := mustExtract(t, "price-list.xlsx", "", xlsxBytes(t, f))
	if doc.Title != "Rates" {
		t.Errorf("title = %q", doc.Title)
	}
	if len(doc.Blocks) != 2 {
		t.Fatalf("blocks = %q (hidden and empty sheets must be skipped)", blockSummary(doc.Blocks))
	}
	h, tb := doc.Blocks[0], doc.Blocks[1]
	if h.Kind != KindHeading || h.Level != 1 || h.Text != "Sheet: Rates" || h.Location != `Sheet "Rates"` {
		t.Errorf("heading = %+v", h)
	}
	if tb.Kind != KindTable || tb.Location != `Sheet "Rates"` {
		t.Fatalf("table block = %+v", tb)
	}
	tbl := tb.Table
	if tbl.Sheet != "Rates" || tbl.FirstRow != 3 || len(tbl.Rows) != 100 {
		t.Errorf("table sheet=%q firstRow=%d rows=%d", tbl.Sheet, tbl.FirstRow, len(tbl.Rows))
	}
	if want := []string{"Bucket", "Rate", "Effective", "Owner"}; fmt.Sprint(tbl.Header) != fmt.Sprint(want) {
		t.Errorf("header = %q, want %q (empty column A dropped)", tbl.Header, want)
	}
	if got := tbl.Rows[0]; fmt.Sprint(got) != fmt.Sprint([]string{"bucket-3", "3%", "2026-03-15", "Ann"}) {
		t.Errorf("row 3 = %q (date and percent must be formatted)", got)
	}
	if got := tbl.Rows[99]; got[0] != "bucket-102" || len(got) != 4 {
		t.Errorf("last row = %q", got)
	}
}

func TestExtractXLSXGenericSheetNameTitle(t *testing.T) {
	f := excelize.NewFile()
	setCell(t, f, "Sheet1", "A1", "Name")
	setCell(t, f, "Sheet1", "A2", "x")
	doc := mustExtract(t, "Q3 price list.xlsx", "", xlsxBytes(t, f))
	if doc.Title != "Q3 price list" {
		t.Errorf("title = %q", doc.Title)
	}
}

func TestExtractXLSXRowCap(t *testing.T) {
	f := excelize.NewFile()
	sw, err := f.NewStreamWriter("Sheet1")
	if err != nil {
		t.Fatal(err)
	}
	for r := 1; r <= MaxSheetRows+2; r++ {
		cellRef, _ := excelize.CoordinatesToCellName(1, r)
		if err := sw.SetRow(cellRef, []any{r}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sw.Flush(); err != nil {
		t.Fatal(err)
	}
	_, err = Extract(t.Context(), "big.xlsx", "", xlsxBytes(t, f))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func ptr[T any](v T) *T { return &v }
