package handler

import "testing"

// The long slice-action procedures now live in embedded .md templates. This
// guards that every expected template is present, non-empty, and carries no
// trailing newline (byte-parity with the old inline base strings — the
// dispatcher appends scope/context directly after).
func TestSliceActionTemplatesLoad(t *testing.T) {
	wires := []string{"run_qa", "auto_docs", "gen_test_cases", "run_test_cases"}
	for _, w := range wires {
		got := sliceActionTemplate(w)
		if got == "" {
			t.Errorf("template %q is empty", w)
		}
		if got[len(got)-1] == '\n' {
			t.Errorf("template %q has a trailing newline (breaks byte-parity)", w)
		}
	}
	// buildSliceInstruction routes the long kinds through the templates.
	if buildSliceInstruction(sliceActionRunQA, "") == "" {
		t.Error("run_qa buildSliceInstruction empty — template not wired")
	}
	if buildSliceInstruction(sliceActionGenTests, "") == "" {
		t.Error("gen_test_cases buildSliceInstruction empty — template not wired")
	}
}
