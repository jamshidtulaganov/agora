package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"
)

func mustExtract(t *testing.T, filename, contentType string, data []byte) Document {
	t.Helper()
	doc, err := Extract(context.Background(), filename, contentType, data)
	if err != nil {
		t.Fatalf("Extract(%s): %v", filename, err)
	}
	return doc
}

// blockSummary renders blocks compactly for comparisons.
func blockSummary(blocks []Block) []string {
	out := make([]string, len(blocks))
	for i, b := range blocks {
		switch b.Kind {
		case KindHeading:
			out[i] = fmt.Sprintf("h%d:%s", b.Level, b.Text)
		case KindTable:
			out[i] = fmt.Sprintf("table:%v/%d rows", b.Table.Header, len(b.Table.Rows))
		default:
			out[i] = b.Kind + ":" + b.Text
		}
		if b.Location != "" {
			out[i] += " @" + b.Location
		}
	}
	return out
}

func TestExtractMarkdown(t *testing.T) {
	src := `---
title: ignored front matter
---
# Collections SOP

Intro paragraph
continues here.

## Write-offs

* first step
* second step
  continued
    - nested

1. one
2. two

| Bucket | Rate \| note | Owner |
|:-------|------:|-------|
| 0-30   | 5%    | Ann   |
| 31-60  | 10%   | Bob   |

` + "```" + `
# not a heading
` + "```" + `

Approval
--------

Text under setext.

---

### **Escalation** [link](http://x)
Last line.
`
	doc := mustExtract(t, "sop.md", "", []byte(src))
	if doc.Title != "Collections SOP" {
		t.Errorf("title = %q", doc.Title)
	}
	want := []string{
		"h1:Collections SOP",
		"paragraph:Intro paragraph\ncontinues here.",
		"h2:Write-offs",
		"list:- first step\n- second step\n  continued\n    - nested",
		"list:1. one\n2. two",
		"table:[Bucket Rate | note Owner]/2 rows",
		"paragraph:```\n# not a heading\n```",
		"h2:Approval",
		"paragraph:Text under setext.",
		"h3:Escalation link",
		"paragraph:Last line.",
	}
	if got := blockSummary(doc.Blocks); !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}
	tbl := doc.Blocks[5].Table
	if !reflect.DeepEqual(tbl.Rows[1], []string{"31-60", "10%", "Bob"}) || tbl.FirstRow != 0 || tbl.Sheet != "" {
		t.Errorf("table = %+v", tbl)
	}
}

func TestExtractMarkdownTitleFallsBackToFilename(t *testing.T) {
	doc := mustExtract(t, "dir/Refund Policy.markdown", "", []byte("## Only H2\n\ntext"))
	if doc.Title != "Refund Policy" {
		t.Errorf("title = %q", doc.Title)
	}
}

func TestExtractText(t *testing.T) {
	src := "Welcome to the team.\r\n\r\n" +
		"1. Scope\r\n\r\n" +
		"This policy covers all collectors.\r\n\r\n" +
		"ESCALATION CONTACTS\r\n\r\n" +
		"Call the lead first.\r\n\r\n" +
		"Short line\r\n\r\n" +
		"Another paragraph that is ordinary.\r\n\r\n" +
		"- bullet one\r\n- bullet two\r\n\r\n" +
		"2. Ends with a period.\r\n\r\n" +
		"Trailing text.\r\n"
	doc := mustExtract(t, "notes.txt", "", []byte(src))
	want := []string{
		"paragraph:Welcome to the team.",
		"h1:1. Scope",
		"paragraph:This policy covers all collectors.",
		"h1:ESCALATION CONTACTS",
		"paragraph:Call the lead first.",
		"paragraph:Short line", // Title-case lines are headings only in PDFs.
		"paragraph:Another paragraph that is ordinary.",
		"list:- bullet one\n- bullet two",
		"paragraph:2. Ends with a period.",
		"paragraph:Trailing text.",
	}
	if got := blockSummary(doc.Blocks); !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}
	if doc.Title != "notes" {
		t.Errorf("title = %q", doc.Title)
	}
}

func TestExtractTextEncodings(t *testing.T) {
	t.Run("invalid UTF-8", func(t *testing.T) {
		_, err := Extract(context.Background(), "bad.txt", "text/plain", []byte("caf\xe9 au lait"))
		if !errors.Is(err, ErrNotUTF8) {
			t.Fatalf("err = %v, want ErrNotUTF8", err)
		}
	})
	t.Run("UTF-16LE with BOM", func(t *testing.T) {
		data := []byte{0xFF, 0xFE}
		for _, r := range "Привет\n" {
			data = append(data, byte(r), byte(r>>8))
		}
		doc := mustExtract(t, "ru.txt", "", data)
		if len(doc.Blocks) != 1 || doc.Blocks[0].Text != "Привет" {
			t.Fatalf("blocks = %q", blockSummary(doc.Blocks))
		}
	})
	t.Run("UTF-8 BOM and NUL stripped", func(t *testing.T) {
		doc := mustExtract(t, "a.md", "", []byte("\xEF\xBB\xBFhello\x00 world"))
		if len(doc.Blocks) != 1 || doc.Blocks[0].Text != "hello world" {
			t.Fatalf("blocks = %q", blockSummary(doc.Blocks))
		}
	})
}

func TestExtractCSV(t *testing.T) {
	src := "Bucket;Rate;Owner;\n0-30;5%;Ann;\n\n\"31;60\";10%;\"Bob\nSmith\";\n61-90;;Cy;\n\n\n"
	doc := mustExtract(t, "rates.csv", "", []byte(src))
	if doc.Title != "rates" || len(doc.Blocks) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	tbl := doc.Blocks[0].Table
	if tbl == nil || doc.Blocks[0].Kind != KindTable {
		t.Fatalf("block = %+v", doc.Blocks[0])
	}
	if !reflect.DeepEqual(tbl.Header, []string{"Bucket", "Rate", "Owner"}) {
		t.Errorf("header = %q (trailing empty column should be dropped)", tbl.Header)
	}
	if tbl.FirstRow != 2 {
		t.Errorf("FirstRow = %d, want 2", tbl.FirstRow)
	}
	// Line 3 is blank, so the quoted row is source row 4 = Rows[2].
	wantRows := [][]string{
		{"0-30", "5%", "Ann"},
		nil,
		{"31;60", "10%", "Bob\nSmith"},
		nil,
		{"61-90", "", "Cy"},
	}
	if !reflect.DeepEqual(tbl.Rows, wantRows) {
		t.Errorf("rows = %q, want %q", tbl.Rows, wantRows)
	}
}

func TestSniffDelimiter(t *testing.T) {
	for _, tc := range []struct {
		first string
		want  rune
	}{
		{"a,b,c", ','},
		{"a;b;c", ';'},
		{"a\tb\tc", '\t'},
		{`"x,y";b;c`, ';'},
		{"single", ','},
	} {
		if got := sniffDelimiter(tc.first + "\n1"); got != tc.want {
			t.Errorf("sniffDelimiter(%q) = %q, want %q", tc.first, got, tc.want)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	for _, tc := range []struct {
		name, ct string
		want     format
		err      error
	}{
		{"a.MD", "", formatMarkdown, nil},
		{"a.txt", "application/octet-stream", formatText, nil},
		{"C:\\docs\\policy.docx", "", formatDOCX, nil},
		{"rates.xlsx", "", formatXLSX, nil},
		{"data.tsv", "", formatCSV, nil},
		{"scan.pdf", "", formatPDF, nil},
		{"upload", "application/pdf", formatPDF, nil},
		{"upload.bin", "text/plain; charset=utf-8", formatText, nil},
		{"old.doc", "application/msword", 0, ErrUnsupported},
		{"old.xls", "", 0, ErrUnsupported},
		{"setup.exe", "application/octet-stream", 0, ErrUnsupported},
		{"noext", "", 0, ErrUnsupported},
	} {
		got, err := detectFormat(tc.name, tc.ct)
		if tc.err != nil {
			if !errors.Is(err, tc.err) {
				t.Errorf("%s: err = %v, want %v", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	if _, err := Extract(context.Background(), "photo.png", "image/png", []byte{1}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Extract png err = %v, want ErrUnsupported", err)
	}
}

func TestExtractLimits(t *testing.T) {
	t.Run("input size", func(t *testing.T) {
		_, err := Extract(context.Background(), "big.txt", "", make([]byte, MaxInputBytes+1))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}
	})
	t.Run("text size", func(t *testing.T) {
		para := strings.Repeat("word ", 1000) + "\n\n"
		_, err := Extract(context.Background(), "big.md", "", []byte(strings.Repeat(para, MaxTextChars/5000+2)))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}
	})
	t.Run("csv text size", func(t *testing.T) {
		row := strings.Repeat("x", 99) + "\n"
		_, err := Extract(context.Background(), "big.csv", "", []byte("h\n"+strings.Repeat(row, MaxTextChars/99+1)))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("err = %v, want ErrTooLarge", err)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Extract(ctx, "a.md", "", []byte("# x")); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// zipWithRaw builds a zip whose single entry declares uncompressedSize but
// holds only a few stored bytes — the shape of a zip bomb's directory.
func zipWithRaw(t *testing.T, name string, uncompressedSize uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	payload := []byte("tiny")
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               name,
		Method:             zip.Store,
		CRC32:              crc32.ChecksumIEEE(payload),
		CompressedSize64:   uint64(len(payload)),
		UncompressedSize64: uncompressedSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestZipBombGuard(t *testing.T) {
	huge := zipWithRaw(t, "word/document.xml", 50<<30)
	for _, name := range []string{"bomb.docx", "bomb.xlsx"} {
		if _, err := Extract(context.Background(), name, "", huge); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: err = %v, want ErrTooLarge", name, err)
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i <= MaxZipEntries; i++ {
		if _, err := zw.Create(fmt.Sprintf("f%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(context.Background(), "many.docx", "", buf.Bytes()); !errors.Is(err, ErrTooLarge) {
		t.Errorf("many entries: err = %v, want ErrTooLarge", err)
	}

	if _, err := Extract(context.Background(), "locked.docx", "", append([]byte{}, cfbMagic...)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("CFB file: err = %v, want ErrUnsupported", err)
	}
	if _, err := Extract(context.Background(), "junk.docx", "", []byte("not a zip")); err == nil {
		t.Error("junk docx: want error")
	}
}
