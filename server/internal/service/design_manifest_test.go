package service

import "testing"

func TestParseDesignContextBlock(t *testing.T) {
	valid := `{"version":1,"kind":"tokens","figma":{},"tokens":{"colors":{"primary":"#fff"},"typography":{},"spacing":{}},"components":[],"conventions":[],"anti_patterns":[],"sources":[{"kind":"repository","locator":"tokens.css","content_hash":"abcdef12","captured_at":"2026-08-11T06:00:00Z"}]}`
	tests := []struct {
		name    string
		content string
		wantOK  bool
	}{
		{"no block", "plain comment", false},
		{"design-context block", "```design-context\n" + valid + "\n```", true},
		{"legacy fence remains readable", "```design-manifest\n" + valid + "\n```", true},
		{"missing provenance", "```design-context\n{\"version\":1,\"kind\":\"tokens\",\"figma\":{},\"tokens\":{\"colors\":{},\"typography\":{},\"spacing\":{}},\"components\":[],\"conventions\":[],\"anti_patterns\":[],\"sources\":[]}\n```", false},
		{"malformed json", "```design-context\n{not json\n```", false},
		{"array is not a context", "```design-context\n[1,2,3]\n```", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := parseDesignContextBlock(tt.content)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}
