package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	// pdfMaxOutputBytes caps pdftotext's stdout. -layout pads with spaces, so
	// this is well above MaxTextChars.
	pdfMaxOutputBytes = 32 << 20
	// scannedCharsPerPage: below this average of non-space characters per
	// page, a PDF is treated as scanned (no usable text layer).
	scannedCharsPerPage = 30
)

var pageNumberLine = regexp.MustCompile(`(?i)^[-–—\s]*(page|p\.|pg\.?|стр\.?|страница|sahifa|seite)?\s*\d{1,4}(\s*(of|из|/|von)\s*\d{1,4})?[-–—\s]*$`)

// extractPDF runs `pdftotext -layout -enc UTF-8 <file> -`, splits the output
// into pages on form feeds, and segments each page into paragraphs, lists
// and (conservatively) headings located "p. N".
func extractPDF(ctx context.Context, data []byte, b *builder) (Document, error) {
	out, err := runPDFToText(ctx, data)
	if err != nil {
		return Document{}, err
	}
	// pdftotext ends every page with a form feed; the text after the last
	// one is empty. Split before sanitizing, which drops control characters.
	pages := strings.Split(out, "\f")
	if len(pages) > 0 && strings.TrimSpace(pages[len(pages)-1]) == "" {
		pages = pages[:len(pages)-1]
	}
	for i, p := range pages {
		pages[i] = sanitize(normalizeNewlines(p))
	}
	doc := Document{PageCount: len(pages)}

	visible := 0
	for _, p := range pages {
		for _, r := range p {
			if !unicode.IsSpace(r) {
				visible++
			}
		}
	}
	if len(pages) == 0 || visible/len(pages) < scannedCharsPerPage {
		doc.Scanned = true
	}

	pageLines := make([][]string, len(pages))
	for i, p := range pages {
		pageLines[i] = strings.Split(p, "\n")
	}
	stripRunningLines(pageLines)

	var segs []plainSeg
	for i, lines := range pageLines {
		if err := ctx.Err(); err != nil {
			return Document{}, err
		}
		segs = append(segs, segmentPlain(strings.Join(lines, "\n"), "p. "+strconv.Itoa(i+1), true)...)
	}
	for _, s := range markPlainHeadings(segs, true) {
		if err := s.add(b); err != nil {
			return Document{}, err
		}
	}
	return doc, nil
}

func runPDFToText(ctx context.Context, data []byte) (string, error) {
	bin, err := exec.LookPath("pdftotext")
	if err != nil {
		return "", ErrPDFToolMissing
	}
	tmp, err := os.CreateTemp("", "agora-knowledge-*.pdf")
	if err != nil {
		return "", fmt.Errorf("extract: temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("extract: temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("extract: temp file: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, PDFTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, "-layout", "-enc", "UTF-8", tmp.Name(), "-")
	stdout := &cappedBuffer{max: pdfMaxOutputBytes}
	stderr := &cappedBuffer{max: 4 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	switch {
	case stdout.overflow:
		return "", fmt.Errorf("%w: PDF text is larger than %d bytes", ErrTooLarge, pdfMaxOutputBytes)
	case ctx.Err() != nil:
		return "", ctx.Err()
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("extract: pdftotext did not finish within %s", PDFTimeout)
	case runErr != nil:
		msg := strings.TrimSpace(stderr.buf.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return "", fmt.Errorf("extract: could not read the PDF (it may be damaged or password-protected): %s", msg)
	}
	return stdout.buf.String(), nil
}

// cappedBuffer collects up to max bytes and then fails writes, which makes
// the child process exit on a broken pipe.
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		c.overflow = true
		return 0, errors.New("output limit exceeded")
	}
	return c.buf.Write(p)
}

// stripRunningLines blanks page numbers and running headers/footers: the
// first/last non-empty line of a page when it is a bare page number, or when
// (in documents of 3+ pages) the same line, digits ignored, opens or closes
// at least half of the pages.
func stripRunningLines(pages [][]string) {
	edge := func(lines []string, fromEnd bool) int {
		for k := range lines {
			i := k
			if fromEnd {
				i = len(lines) - 1 - k
			}
			if strings.TrimSpace(lines[i]) != "" {
				return i
			}
		}
		return -1
	}
	key := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		return strings.Map(func(r rune) rune {
			if unicode.IsDigit(r) {
				return '#'
			}
			return r
		}, s)
	}
	type pos struct{ top, bottom int }
	edges := make([]pos, len(pages))
	topCount, bottomCount := map[string]int{}, map[string]int{}
	for i, lines := range pages {
		edges[i] = pos{edge(lines, false), edge(lines, true)}
		if edges[i].top >= 0 {
			topCount[key(lines[edges[i].top])]++
			if edges[i].bottom != edges[i].top {
				bottomCount[key(lines[edges[i].bottom])]++
			}
		}
	}
	threshold := max(2, (len(pages)+1)/2)
	for i, lines := range pages {
		for _, e := range []struct {
			idx    int
			counts map[string]int
		}{{edges[i].top, topCount}, {edges[i].bottom, bottomCount}} {
			if e.idx < 0 || lines[e.idx] == "" {
				continue
			}
			line := strings.TrimSpace(lines[e.idx])
			if pageNumberLine.MatchString(line) || (len(pages) >= 3 && e.counts[key(line)] >= threshold) {
				lines[e.idx] = ""
			}
		}
	}
}
