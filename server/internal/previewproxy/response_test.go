package previewproxy

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPrepareResponseLeavesBinaryBodyAndRemovesFrameBlockers(t *testing.T) {
	raw := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	resp := &http.Response{
		Header: http.Header{
			"Content-Type":            {"image/png"},
			"Content-Security-Policy": {"default-src 'self'; frame-ancestors 'none'; img-src data:"},
			"X-Frame-Options":         {"DENY"},
		},
		Body: io.NopCloser(strings.NewReader(string(raw))),
	}
	if err := PrepareResponse(resp, "/editor/local/5173"); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	if string(got) != string(raw) {
		t.Errorf("image body mutated: %v", got)
	}
	if got := resp.Header.Get("Content-Security-Policy"); got != "default-src 'self'; img-src data:" {
		t.Errorf("non-framing CSP changed: %q", got)
	}
	if resp.Header.Get("X-Frame-Options") != "" {
		t.Errorf("X-Frame-Options remains: %v", resp.Header)
	}
}
