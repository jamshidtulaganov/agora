package handler

import (
	"strings"
	"testing"
)

// TestCompileInstructionRequiresBrowserDrivenUICases asserts the compile_tests
// directive forces a UI / [e2e] case to actually DRIVE the browser (connectOverCDP
// to the shared review browser + real page interactions) rather than shortcutting
// it with a raw fetch of the HTML. That shortcut is exactly why the first live
// runs authored HTTP/filesystem assertions — nothing ever opened in the review
// page's live pane. Pure API/[api] cases may still use HTTP. Pure (no DB).
func TestCompileInstructionRequiresBrowserDrivenUICases(t *testing.T) {
	s := buildSliceInstruction(sliceActionCompileTests, "")
	for _, want := range []string{
		"connectOverCDP",         // attach to the SHARED review browser (watchable live)
		"BROWSER-DRIVE UI CASES", // the explicit requirement
		"page.goto",              // real navigation, not a raw fetch
		"[e2e]",                  // UI cases MUST browser-drive
		"[api]",                  // pure API/data cases may stay HTTP
	} {
		if !strings.Contains(s, want) {
			t.Errorf("compile_tests instruction missing %q", want)
		}
	}
}
