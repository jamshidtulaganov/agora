package chunk

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jamshidtulaganov/agora/server/internal/knowledge/extract"
)

func h(level int, text string) extract.Block {
	return extract.Block{Kind: extract.KindHeading, Level: level, Text: text}
}

func p(text string) extract.Block {
	return extract.Block{Kind: extract.KindParagraph, Text: text}
}

func pAt(text, loc string) extract.Block {
	return extract.Block{Kind: extract.KindParagraph, Text: text, Location: loc}
}

type row struct{ path, loc, body string }

func rows(chunks []Chunk) []row {
	out := make([]row, len(chunks))
	for i, c := range chunks {
		if c.Ord != i {
			panic(fmt.Sprintf("chunk %d has Ord %d", i, c.Ord))
		}
		out[i] = row{c.HeadingPath, c.Location, c.Body}
	}
	return out
}

func TestSplitHeadingPaths(t *testing.T) {
	doc := extract.Document{
		Title: "Collections SOP",
		Blocks: []extract.Block{
			p("Preamble before any heading."),
			h(1, "Collections SOP"),
			p("Intro."),
			h(2, "Write-offs"),
			h(3, "Approval"),
			p("Needs two signatures."),
			h(4, "Exceptions"),
			p("Under $50 needs one."),
			h(3, "Records"),
			h(2, "Disputes"),
			p("   "),
			p("Log every dispute."),
			h(1, "Appendix"),
		},
	}
	got := rows(Split(doc, Options{}))
	want := []row{
		{"Collections SOP", "", "Preamble before any heading."},
		{"Collections SOP", "", "Intro."},
		// "Write-offs" has no own text but its sub-section does: it only
		// appears in the path. "Exceptions" (level 4) stays in the body.
		{"Collections SOP › Write-offs › Approval", "", "Needs two signatures.\n\n#### Exceptions\n\nUnder $50 needs one."},
		// Empty leaf sections keep their heading as the body.
		{"Collections SOP › Write-offs › Records", "", "### Records"},
		{"Collections SOP › Disputes", "", "Log every dispute."},
		{"Collections SOP › Appendix", "", "# Appendix"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chunks:\n got %q\nwant %q", got, want)
	}
}

func TestSplitTitleOnlyDocument(t *testing.T) {
	doc := extract.Document{Title: "Glossary", Blocks: []extract.Block{h(1, "Glossary")}}
	if got := Split(doc, Options{}); len(got) != 0 {
		t.Errorf("chunks = %q, want none (title is in every path)", rows(got))
	}
}

func TestSplitTable(t *testing.T) {
	tbl := &extract.Table{Header: []string{"Bucket", "Rate|%"}, Sheet: "Rates", FirstRow: 2}
	for i := 0; i < 100; i++ {
		tbl.Rows = append(tbl.Rows, []string{fmt.Sprintf("b%d", i+2), "5\nflat"})
	}
	tbl.Rows[50] = nil // an empty source row (row 52) is skipped but keeps numbering
	doc := extract.Document{
		Title: "Price list",
		Blocks: []extract.Block{
			{Kind: extract.KindHeading, Level: 1, Text: "Sheet: Rates", Location: `Sheet "Rates"`},
			{Kind: extract.KindTable, Table: tbl, Location: `Sheet "Rates"`},
		},
	}
	chunks := Split(doc, Options{TableRowsPerChunk: 40})
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks: %q", len(chunks), rows(chunks))
	}
	wantLocs := []string{`Sheet "Rates", rows 2–41`, `Sheet "Rates", rows 42–82`, `Sheet "Rates", rows 83–101`}
	wantRows := []int{40, 40, 19}
	const header = "| Bucket | Rate\\|% |\n| --- | --- |\n"
	for i, c := range chunks {
		if c.HeadingPath != "Price list › Sheet: Rates" {
			t.Errorf("chunk %d path = %q", i, c.HeadingPath)
		}
		if c.Location != wantLocs[i] {
			t.Errorf("chunk %d location = %q, want %q", i, c.Location, wantLocs[i])
		}
		if !strings.HasPrefix(c.Body, header) {
			t.Errorf("chunk %d does not start with the header:\n%s", i, c.Body)
		}
		if n := strings.Count(c.Body, "\n") - 1; n != wantRows[i] {
			t.Errorf("chunk %d has %d rows, want %d", i, n, wantRows[i])
		}
	}
	if !strings.Contains(chunks[0].Body, "| b2 | 5<br>flat |") {
		t.Errorf("cell newline not rendered as <br>:\n%s", chunks[0].Body)
	}
}

func TestSplitTableLocations(t *testing.T) {
	for _, tc := range []struct {
		name string
		blk  extract.Block
		want []string
	}{
		{
			name: "csv rows",
			blk:  extract.Block{Kind: extract.KindTable, Table: &extract.Table{Header: []string{"a"}, Rows: [][]string{{"1"}, {"2"}}, FirstRow: 2}},
			want: []string{"rows 2–3"},
		},
		{
			name: "single row",
			blk:  extract.Block{Kind: extract.KindTable, Table: &extract.Table{Header: []string{"a"}, Rows: [][]string{{"1"}}, Sheet: "S", FirstRow: 5}},
			want: []string{`Sheet "S", row 5`},
		},
		{
			name: "docx table",
			blk:  extract.Block{Kind: extract.KindTable, Table: &extract.Table{Header: []string{"a"}, Rows: [][]string{{"1"}}}},
			want: []string{""},
		},
		{
			name: "header only",
			blk:  extract.Block{Kind: extract.KindTable, Table: &extract.Table{Header: []string{"a", "b"}, Sheet: "S", FirstRow: 2}},
			want: []string{`Sheet "S"`},
		},
		{
			name: "empty table",
			blk:  extract.Block{Kind: extract.KindTable, Table: &extract.Table{}},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, c := range Split(extract.Document{Title: "T", Blocks: []extract.Block{tc.blk}}, Options{}) {
				got = append(got, c.Location)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("locations = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitTableRespectsMaxChars(t *testing.T) {
	tbl := &extract.Table{Header: []string{"k", "v"}, FirstRow: 2}
	for i := 0; i < 10; i++ {
		tbl.Rows = append(tbl.Rows, []string{fmt.Sprint(i), strings.Repeat("x", 90)})
	}
	chunks := Split(extract.Document{Title: "T", Blocks: []extract.Block{{Kind: extract.KindTable, Table: tbl}}},
		Options{TargetChars: 200, MaxChars: 350, TableRowsPerChunk: 40})
	if len(chunks) < 4 {
		t.Fatalf("got %d chunks, want the 10 wide rows spread over several", len(chunks))
	}
	for _, c := range chunks {
		if n := utf8.RuneCountInString(c.Body); n > 350 {
			t.Errorf("chunk %q is %d chars", c.Location, n)
		}
	}
}

func TestSplitPageRanges(t *testing.T) {
	doc := extract.Document{
		Title: "Policy",
		Blocks: []extract.Block{
			pAt("Page three text.", "p. 3"),
			pAt("Continues on page four.", "p. 4"),
			{Kind: extract.KindHeading, Level: 2, Text: "Next", Location: "p. 5"},
			pAt("Only page five.", "p. 5"),
			{Kind: extract.KindHeading, Level: 2, Text: "Empty", Location: "p. 6"},
		},
	}
	got := rows(Split(doc, Options{}))
	want := []row{
		{"Policy", "pp. 3–4", "Page three text.\n\nContinues on page four."},
		{"Policy › Next", "p. 5", "Only page five."},
		{"Policy › Empty", "p. 6", "## Empty"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chunks:\n got %q\nwant %q", got, want)
	}
}

func TestSplitMergeAndSplitThresholds(t *testing.T) {
	opts := Options{TargetChars: 100, MaxChars: 150}
	forty := strings.Repeat("a", 39) + "."

	t.Run("paragraphs merge up to target", func(t *testing.T) {
		doc := extract.Document{Title: "T", Blocks: []extract.Block{p(forty), p(forty), p(forty), p(forty), p(forty)}}
		var sizes []int
		for _, c := range Split(doc, opts) {
			sizes = append(sizes, strings.Count(c.Body, forty))
		}
		// 40+2+40 = 82 fits; a third would make 124 > 100.
		if !reflect.DeepEqual(sizes, []int{2, 2, 1}) {
			t.Errorf("paragraphs per chunk = %v", sizes)
		}
	})

	t.Run("paragraph between target and max is kept whole", func(t *testing.T) {
		long := strings.Repeat("word ", 25) + "end." // 129 chars
		chunks := Split(extract.Document{Title: "T", Blocks: []extract.Block{p("short."), p(long)}}, opts)
		if len(chunks) != 2 || chunks[1].Body != long {
			t.Errorf("chunks = %q", rows(chunks))
		}
	})

	t.Run("paragraph over max splits on sentences", func(t *testing.T) {
		var sentences []string
		for i := 0; i < 6; i++ {
			sentences = append(sentences, fmt.Sprintf("Sentence number %d is here.", i))
		}
		text := strings.Join(sentences, " ") // 6 x 27 + 5 = 167 > 150
		chunks := Split(extract.Document{Title: "T", Blocks: []extract.Block{p(text)}}, opts)
		if len(chunks) != 2 {
			t.Fatalf("chunks = %q", rows(chunks))
		}
		for _, c := range chunks {
			if !strings.HasSuffix(c.Body, "is here.") || !strings.HasPrefix(c.Body, "Sentence number") {
				t.Errorf("not cut on a sentence boundary: %q", c.Body)
			}
		}
		if joined := chunks[0].Body + " " + chunks[1].Body; joined != text {
			t.Errorf("text lost: %q", joined)
		}
	})

	t.Run("no sentence boundaries: hard split", func(t *testing.T) {
		blob := strings.Repeat("x", 400)
		chunks := Split(extract.Document{Title: "T", Blocks: []extract.Block{p(blob)}}, opts)
		total := 0
		for _, c := range chunks {
			n := utf8.RuneCountInString(c.Body)
			if n > opts.MaxChars {
				t.Errorf("piece of %d chars", n)
			}
			total += n
		}
		if total != 400 {
			t.Errorf("total = %d, want 400", total)
		}
	})

	t.Run("long list splits on items", func(t *testing.T) {
		var items []string
		for i := 0; i < 8; i++ {
			items = append(items, fmt.Sprintf("- item %d with some words\n  continued", i))
		}
		list := extract.Block{Kind: extract.KindList, Text: strings.Join(items, "\n")}
		for _, c := range Split(extract.Document{Title: "T", Blocks: []extract.Block{list}}, opts) {
			if !strings.HasPrefix(c.Body, "- item") || !strings.HasSuffix(c.Body, "continued") {
				t.Errorf("list cut inside an item: %q", c.Body)
			}
		}
	})

	t.Run("trailing minor heading moves to the next chunk", func(t *testing.T) {
		doc := extract.Document{Title: "T", Blocks: []extract.Block{p(forty), h(4, "Sub"), p(strings.Repeat("b", 60))}}
		got := rows(Split(doc, opts))
		want := []row{{"T", "", forty}, {"T › Sub", "", strings.Repeat("b", 60)}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("chunks = %q", got)
		}
	})
}

func TestSplitDeterministic(t *testing.T) {
	src := "# Guide\n\nIntro text.\n\n## A\n\n" + strings.Repeat("Some sentence here. ", 400) +
		"\n\n| k | v |\n|---|---|\n| 1 | 2 |\n\n### A.1\n\n- x\n- y\n"
	doc, err := extract.Extract(context.Background(), "guide.md", "", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	first := Split(doc, Options{})
	for i := 0; i < 5; i++ {
		if again := Split(doc, Options{}); !reflect.DeepEqual(first, again) {
			t.Fatal("Split is not deterministic")
		}
	}
	var paths []string
	for _, c := range first {
		if utf8.RuneCountInString(c.Body) > DefaultMaxChars {
			t.Errorf("chunk %d exceeds MaxChars", c.Ord)
		}
		paths = append(paths, c.HeadingPath)
	}
	want := []string{"Guide", "Guide › A", "Guide › A", "Guide › A", "Guide › A", "Guide › A › A.1"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %q, want %q", paths, want)
	}
	if !strings.HasPrefix(first[4].Body, "| k | v |") {
		t.Errorf("table chunk = %q", first[4].Body)
	}
}

func TestSplitDropsLeadingTitleParagraph(t *testing.T) {
	doc := extract.Document{Title: "Collections SOP", Blocks: []extract.Block{
		{Kind: extract.KindParagraph, Text: "Collections SOP"},
		{Kind: extract.KindHeading, Level: 2, Text: "Write-offs"},
		{Kind: extract.KindParagraph, Text: "Write-offs above $5,000 need approval."},
	}}
	chunks := Split(doc, Options{})
	if len(chunks) != 1 || !strings.Contains(chunks[0].Body, "Write-offs above") {
		t.Fatalf("chunks = %+v", chunks)
	}
}
