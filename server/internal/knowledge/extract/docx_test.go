package extract

import (
	"archive/zip"
	"bytes"
	"reflect"
	"strings"
	"testing"
)

const wNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func buildDOCX(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/numbering.xml", "docProps/core.xml"}
	for _, name := range names {
		content, ok := parts[name]
		if !ok {
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func para(style, text string) string {
	ppr := ""
	if style != "" {
		ppr = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + ppr + `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
}

func listPara(numID, ilvl, text string) string {
	return `<w:p><w:pPr><w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="` + ilvl + `"/><w:numId w:val="` + numID + `"/></w:numPr></w:pPr><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func cell(text, tcPr string) string {
	return `<w:tc>` + tcPr + para("", text) + `</w:tc>`
}

func docxParts(body string) map[string]string {
	return map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document ` + wNS + `><w:body>` + body + `<w:sectPr/></w:body></w:document>`,
		// Heading 2 uses a localized style id ("2"), as Russian Word writes
		// it; detection must go by the style name.
		"word/styles.xml": `<?xml version="1.0" encoding="UTF-8"?><w:styles ` + wNS + `>
<w:style w:type="paragraph" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:outlineLvl w:val="0"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="MyChapter"><w:name w:val="My Chapter"/><w:basedOn w:val="Heading1"/></w:style>
<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/></w:style>
<w:style w:type="paragraph" w:styleId="TOC1"><w:name w:val="toc 1"/></w:style>
</w:styles>`,
		"word/numbering.xml": `<?xml version="1.0" encoding="UTF-8"?><w:numbering ` + wNS + `>
<w:abstractNum w:abstractNumId="10"><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/></w:lvl><w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="lowerLetter"/></w:lvl></w:abstractNum>
<w:abstractNum w:abstractNumId="20"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>
<w:num w:numId="1"><w:abstractNumId w:val="10"/></w:num>
<w:num w:numId="2"><w:abstractNumId w:val="20"/></w:num>
</w:numbering>`,
		"docProps/core.xml": `<?xml version="1.0" encoding="UTF-8"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Metadata title</dc:title></cp:coreProperties>`,
	}
}

func TestExtractDOCX(t *testing.T) {
	body := para("Title", "Collections SOP") +
		para("TOC1", "Write-offs\t3") +
		para("Heading1", "Write-offs") +
		`<w:p><w:r><w:t>Before</w:t></w:r><w:r><w:tab/><w:t>tab</w:t><w:br/><w:t>after break</w:t></w:r>` +
		`<w:r><w:drawing><w:t>image text</w:t></w:drawing></w:r><w:del><w:r><w:delText>deleted</w:delText></w:r></w:del>` +
		`<w:hyperlink><w:r><w:t xml:space="preserve"> linked</w:t></w:r></w:hyperlink></w:p>` +
		para("2", "Approval") +
		listPara("1", "0", "Check the balance") +
		listPara("1", "1", "Confirm the age") +
		listPara("1", "0", "Get sign-off") +
		`<w:p/>` +
		listPara("2", "0", "Bullet item") +
		`<w:tbl><w:tblPr/><w:tblGrid/>` +
		`<w:tr>` + cell("Bucket", "") + cell("Rate", "") + cell("Owner", "") + `</w:tr>` +
		`<w:tr>` + cell("0-30", `<w:tcPr><w:vMerge w:val="restart"/></w:tcPr>`) + cell("5%", "") + cell("Ann", "") + `</w:tr>` +
		`<w:tr>` + cell("", `<w:tcPr><w:vMerge/></w:tcPr>`) + cell("Flat fee | extra", `<w:tcPr><w:gridSpan w:val="2"/></w:tcPr>`) + `</w:tr>` +
		`</w:tbl>` +
		para("MyChapter", "Disputes") +
		para("", "Plain closing paragraph.")
	doc := mustExtract(t, "sop.docx", "", buildDOCX(t, docxParts(body)))

	if doc.Title != "Collections SOP" {
		t.Errorf("title = %q", doc.Title)
	}
	want := []string{
		"h1:Collections SOP",
		"h1:Write-offs",
		"paragraph:Before tab\nafter break linked",
		"h2:Approval",
		"list:1. Check the balance\n  a. Confirm the age\n2. Get sign-off\n- Bullet item",
		"table:[Bucket Rate Owner]/2 rows",
		"h1:Disputes",
		"paragraph:Plain closing paragraph.",
	}
	if got := blockSummary(doc.Blocks); !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}
	for _, b := range doc.Blocks {
		if b.Kind == KindTable {
			wantRows := [][]string{{"0-30", "5%", "Ann"}, {"0-30", "Flat fee | extra", ""}}
			if !reflect.DeepEqual(b.Table.Rows, wantRows) {
				t.Errorf("table rows = %q, want %q", b.Table.Rows, wantRows)
			}
		}
	}
}

func TestExtractDOCXTitleFallbacks(t *testing.T) {
	parts := docxParts(para("Heading1", "First Heading") + para("", "text"))
	doc := mustExtract(t, "a.docx", "", buildDOCX(t, parts))
	if doc.Title != "Metadata title" {
		t.Errorf("with core.xml: title = %q", doc.Title)
	}
	delete(parts, "docProps/core.xml")
	doc = mustExtract(t, "a.docx", "", buildDOCX(t, parts))
	if doc.Title != "First Heading" {
		t.Errorf("without core.xml: title = %q", doc.Title)
	}
	parts["word/document.xml"] = strings.Replace(parts["word/document.xml"], "Heading1", "Normal", 1)
	doc = mustExtract(t, "My Policy.docx", "", buildDOCX(t, parts))
	if doc.Title != "My Policy" {
		t.Errorf("no headings: title = %q", doc.Title)
	}
}

func TestExtractDOCXBoldHeadings(t *testing.T) {
	bold := func(text string) string {
		return `<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>` + text + `</w:t></w:r></w:p>`
	}
	body := bold("Purpose") + para("", "Why this exists.") +
		bold("Not a heading.") + para("", "Body.") +
		bold("Address line one") + bold("Address line two") + para("", "Body again.")
	doc := mustExtract(t, "b.docx", "", buildDOCX(t, docxParts(body)))
	want := []string{
		"h2:Purpose",
		"paragraph:Why this exists.",
		"paragraph:Not a heading.",
		"paragraph:Body.",
		"paragraph:Address line one",
		"h2:Address line two",
		"paragraph:Body again.",
	}
	if got := blockSummary(doc.Blocks); !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %q\nwant %q", got, want)
	}

	// With styled headings present, bold paragraphs stay paragraphs.
	body = para("Heading1", "Real") + bold("Purpose") + para("", "Why.")
	doc = mustExtract(t, "b.docx", "", buildDOCX(t, docxParts(body)))
	if got := blockSummary(doc.Blocks); got[1] != "paragraph:Purpose" {
		t.Errorf("blocks = %q", got)
	}
}

func TestExtractDOCXMissingDocument(t *testing.T) {
	parts := docxParts("")
	delete(parts, "word/document.xml")
	if _, err := Extract(t.Context(), "x.docx", "", buildDOCX(t, parts)); err == nil {
		t.Fatal("want error for missing word/document.xml")
	}
}
