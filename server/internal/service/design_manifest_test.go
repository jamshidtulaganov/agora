package service

import "testing"

func TestParseDesignManifestBlock(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantOK  bool
	}{
		{"no block", "plain comment", false},
		{"tokens manifest", "```design-manifest\n{\"kind\":\"tokens\",\"tokens\":{\"colors\":{\"primary\":\"#fff\"}}}\n```", true},
		{"inventory manifest", "```design-manifest\n{\"kind\":\"inventory\",\"components\":[{\"name\":\"X\"}]}\n```", true},
		{"components-only is valid", "```design-manifest\n{\"components\":[]}\n```", true},
		{"object with none of kind/tokens/components is not a manifest", "```design-manifest\n{\"foo\":1}\n```", false},
		{"malformed json", "```design-manifest\n{not json\n```", false},
		{"array is not a manifest", "```design-manifest\n[1,2,3]\n```", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := parseDesignManifestBlock(tt.content)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestProjectManifestMeta(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		wantSrc  string
		wantRev  int
	}{
		{"empty", "", "", 0},
		{"no manifest key", `{"qa_manifest":{}}`, "", 0},
		{"manual manifest rev 5", `{"design_manifest":{"source":"manual","revision":5}}`, "manual", 5},
		{"agent manifest rev 2", `{"design_manifest":{"source":"agent","revision":2}}`, "agent", 2},
		{"malformed", `{bad`, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, rev := projectManifestMeta([]byte(tt.settings))
			if src != tt.wantSrc || rev != tt.wantRev {
				t.Errorf("got (%q, %d), want (%q, %d)", src, rev, tt.wantSrc, tt.wantRev)
			}
		})
	}
}
