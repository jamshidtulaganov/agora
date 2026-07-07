package util

import "testing"

func TestParseWorkspaceLabs(t *testing.T) {
	// Absent / empty / malformed → defaults, never an error path.
	for _, blob := range [][]byte{nil, []byte(``), []byte(`{`), []byte(`{"labs": 5}`)} {
		labs := ParseWorkspaceLabs(blob)
		if !labs.QADevBoxes || labs.QADevRuntimes || labs.QADevRuntimesStrict || labs.QAFallbackBoxID != "" {
			t.Fatalf("defaults broken for %q: %+v", blob, labs)
		}
	}

	labs := ParseWorkspaceLabs([]byte(`{"labs":{"qa_dev_boxes":false,"qa_fallback_box_id":" b1 ","qa_dev_runtimes":true,"qa_dev_runtimes_strict":true}}`))
	if labs.QADevBoxes || !labs.QADevRuntimes || !labs.QADevRuntimesStrict || labs.QAFallbackBoxID != "b1" {
		t.Fatalf("explicit values not honored: %+v", labs)
	}

	// Partial block: set fields win, absent fields keep defaults.
	labs = ParseWorkspaceLabs([]byte(`{"labs":{"qa_dev_runtimes":true}}`))
	if !labs.QADevBoxes || !labs.QADevRuntimes || labs.QADevRuntimesStrict {
		t.Fatalf("partial block defaults broken: %+v", labs)
	}
}

func TestDevAppURL(t *testing.T) {
	meta := []byte(`{"editor_port":20038,"dev_apps":{"p-1":" http://127.0.0.1:8081 ","p-2":""}}`)
	if got := DevAppURL(meta, "p-1"); got != "http://127.0.0.1:8081" {
		t.Fatalf("p-1: %q", got)
	}
	for _, c := range []struct{ meta, pid string }{
		{string(meta), "p-2"}, {string(meta), "p-3"}, {string(meta), ""}, {"", "p-1"}, {"{bad", "p-1"},
	} {
		if got := DevAppURL([]byte(c.meta), c.pid); got != "" {
			t.Fatalf("expected empty for (%q,%q), got %q", c.meta, c.pid, got)
		}
	}
}
