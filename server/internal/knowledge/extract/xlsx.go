package extract

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// genericSheetName matches default sheet names ("Sheet1", "Лист1", …), which
// make a poor document title.
var genericSheetName = regexp.MustCompile(`(?i)^(sheet|лист|feuil|tabelle|hoja|foglio|planilha|blad|arkusz|varaq)\s*\d*$`)

func extractXLSX(ctx context.Context, data []byte, b *builder) (Document, error) {
	if _, err := openZip(data, "Excel workbook"); err != nil {
		return Document{}, err
	}
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: MaxUnzippedBytes})
	if err != nil {
		return Document{}, fmt.Errorf("extract: not a valid Excel workbook: %w", err)
	}
	defer f.Close()

	title := ""
	if props, err := f.GetDocProps(); err == nil && props != nil {
		title = strings.TrimSpace(sanitize(props.Title))
	}
	firstSheet := ""
	for _, name := range f.GetSheetList() {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		visible, err := f.GetSheetVisible(name)
		if err != nil {
			return Document{}, fmt.Errorf("extract: sheet %q: %w", name, err)
		}
		if !visible {
			continue
		}
		t, err := readSheet(ctx, f, name, b)
		if err != nil {
			return Document{}, err
		}
		if t == nil {
			continue
		}
		if firstSheet == "" {
			firstSheet = name
		}
		loc := sheetLocation(name)
		if err := b.addHeading(1, "Sheet: "+name, loc); err != nil {
			return Document{}, err
		}
		b.addCharged(Block{Kind: KindTable, Table: t, Location: loc})
	}
	if title == "" && !genericSheetName.MatchString(firstSheet) {
		title = firstSheet
	}
	return Document{Title: title}, nil
}

func sheetLocation(name string) string {
	return `Sheet "` + name + `"`
}

// readSheet reads one worksheet into a table: the first non-empty row is the
// header, trailing empty rows are dropped, and columns empty in every row are
// removed. It returns nil for an empty sheet.
func readSheet(ctx context.Context, f *excelize.File, name string, b *builder) (*Table, error) {
	rows, err := f.Rows(name)
	if err != nil {
		return nil, fmt.Errorf("extract: sheet %q: %w", name, err)
	}
	defer rows.Close()

	var header []string
	headerRow := 0
	var data [][]string
	// Empty rows are only materialized when a non-empty row follows, so a
	// sheet whose formatting reaches row 1,048,576 costs nothing.
	pendingEmpty := 0
	for rowNum := 1; rows.Next(); rowNum++ {
		if rowNum%512 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("extract: sheet %q row %d: %w", name, rowNum, err)
		}
		cells, n := cleanCells(cols)
		if header == nil {
			if n > 0 {
				header, headerRow = cells, rowNum
				if err := b.charge(n); err != nil {
					return nil, err
				}
			}
			continue
		}
		if n == 0 {
			pendingEmpty++
			continue
		}
		if rowNum-headerRow > MaxSheetRows {
			return nil, fmt.Errorf("%w: sheet %q has more than %d rows", ErrTooLarge, name, MaxSheetRows)
		}
		if err := b.charge(n); err != nil {
			return nil, err
		}
		for ; pendingEmpty > 0; pendingEmpty-- {
			data = append(data, nil)
		}
		data = append(data, cells)
	}
	if err := rows.Error(); err != nil {
		return nil, fmt.Errorf("extract: sheet %q: %w", name, err)
	}
	if header == nil {
		return nil, nil
	}
	return compactTable(&Table{Header: header, Rows: data, Sheet: name, FirstRow: headerRow + 1}), nil
}

// cleanCells trims and sanitizes cell values and returns the rune count of
// the non-empty ones (0 means the row is empty).
func cleanCells(cols []string) ([]string, int) {
	n := 0
	out := make([]string, len(cols))
	for i, c := range cols {
		c = strings.TrimSpace(sanitize(normalizeNewlines(c)))
		out[i] = c
		n += utf8.RuneCountInString(c)
	}
	return out, n
}

// compactTable drops columns that are empty in the header and every row and
// pads all rows to the same width. Interior empty rows stay (as nil) so
// FirstRow+index remains the source row number.
func compactTable(t *Table) *Table {
	width := len(t.Header)
	for _, r := range t.Rows {
		width = max(width, len(r))
	}
	keep := make([]bool, width)
	for c := 0; c < width; c++ {
		if c < len(t.Header) && t.Header[c] != "" {
			keep[c] = true
			continue
		}
		for _, r := range t.Rows {
			if c < len(r) && r[c] != "" {
				keep[c] = true
				break
			}
		}
	}
	pick := func(r []string) []string {
		if r == nil {
			return nil
		}
		out := make([]string, 0, width)
		for c := 0; c < width; c++ {
			if !keep[c] {
				continue
			}
			v := ""
			if c < len(r) {
				v = r[c]
			}
			out = append(out, v)
		}
		return out
	}
	t.Header = pick(t.Header)
	for i, r := range t.Rows {
		t.Rows[i] = pick(r)
	}
	return t
}
