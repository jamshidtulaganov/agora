package extract

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// extractCSV reads a CSV/TSV file into one table. The delimiter (comma,
// semicolon or tab) is sniffed from the first line. Row numbers are source
// line numbers, so a citation matches what a text editor or Excel shows for
// files without multi-line quoted cells.
func extractCSV(ctx context.Context, data []byte, b *builder) (Document, error) {
	src, err := decodeText(data)
	if err != nil {
		return Document{}, err
	}
	r := csv.NewReader(strings.NewReader(src))
	r.Comma = sniffDelimiter(src)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1

	var header []string
	var rows [][]string
	firstRow := 0
	for n := 0; ; n++ {
		if n%512 == 0 {
			if err := ctx.Err(); err != nil {
				return Document{}, err
			}
		}
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Document{}, fmt.Errorf("extract: CSV: %w", err)
		}
		line, _ := r.FieldPos(0)
		cells, chars := cleanCells(rec)
		if chars == 0 {
			continue
		}
		if err := b.charge(chars); err != nil {
			return Document{}, err
		}
		if header == nil {
			header, firstRow = cells, line+1
			continue
		}
		idx := line - firstRow
		if idx >= MaxSheetRows {
			return Document{}, fmt.Errorf("%w: CSV has more than %d rows", ErrTooLarge, MaxSheetRows)
		}
		// Blank lines (skipped by encoding/csv) become empty rows so
		// FirstRow+index stays the source line.
		for len(rows) < idx {
			rows = append(rows, nil)
		}
		rows = append(rows, cells)
	}
	if header == nil {
		return Document{}, nil
	}
	b.addCharged(Block{Kind: KindTable, Table: compactTable(&Table{Header: header, Rows: rows, FirstRow: firstRow})})
	return Document{}, nil
}

// sniffDelimiter picks the most frequent of , ; \t outside quotes on the
// first line; comma wins ties and single-column files.
func sniffDelimiter(src string) rune {
	first, _, _ := strings.Cut(src, "\n")
	counts := map[rune]int{}
	inQuote := false
	for _, c := range first {
		switch {
		case c == '"':
			inQuote = !inQuote
		case !inQuote && (c == ',' || c == ';' || c == '\t'):
			counts[c]++
		}
	}
	best := ','
	for _, c := range []rune{';', '\t'} {
		if counts[c] > counts[best] {
			best = c
		}
	}
	return best
}
