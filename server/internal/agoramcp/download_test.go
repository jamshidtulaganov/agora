package agoramcp

import (
	"encoding/json"
	"testing"
)

func TestSplitInlineImage(t *testing.T) {
	text, image, mimeType := splitInlineImage(json.RawMessage(`{"filename":"a.png","inline_image_type":"image/png","content_base64":"iVBORw0KGgo="}`))
	if string(image) != "\x89PNG\r\n\x1a\n" || mimeType != "image/png" || text != `{"filename":"a.png","inline_image_type":"image/png"}` {
		t.Fatalf("image result: %q %q %q", text, image, mimeType)
	}
	// Anything that is not a well-formed inline image passes through as text.
	for _, raw := range []string{
		`{"filename":"a.txt","content_text":"ok"}`,
		`{"inline_image_type":"image/svg+xml","content_base64":"PHN2Zz4="}`,
		`{"inline_image_type":"image/png","content_base64":null}`,
		`{"inline_image_type":"image/png","content_base64":"not base64"}`,
		`[]`,
	} {
		if text, image, _ := splitInlineImage(json.RawMessage(raw)); text != raw || image != nil {
			t.Errorf("%s: got %q with image %q", raw, text, image)
		}
	}
}
