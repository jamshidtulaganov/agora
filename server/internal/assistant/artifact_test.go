package assistant

import (
	"strings"
	"testing"
)

// The artifact content contract. Every case here is a correction the MODEL
// will read, so each assertion checks the FIELD NAME is in the message — a
// rejection that does not say which field is wrong costs a round trip and
// teaches the model nothing.

func TestArtifactKindsAreTheFour(t *testing.T) {
	for _, kind := range []string{ArtifactKindChart, ArtifactKindTable, ArtifactKindMarkdown, ArtifactKindHTML} {
		if !IsArtifactKind(kind) {
			t.Fatalf("%q is not recognised as a kind", kind)
		}
	}
	for _, kind := range []string{"", "Chart", "svg", "image", "dashboard"} {
		if IsArtifactKind(kind) {
			t.Fatalf("%q must not be a kind", kind)
		}
	}
	err := ValidateArtifactKind("svg")
	if err == nil {
		t.Fatal("an unknown kind must be rejected")
	}
	// The refusal has to list what IS available, or the model guesses again.
	for _, want := range ArtifactKinds {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("kind error %q does not name %q", err.Error(), want)
		}
	}
}

func TestArtifactTitleIsRequiredAndBounded(t *testing.T) {
	if err := ValidateArtifactTitle("   "); err == nil {
		t.Fatal("a blank title must be rejected")
	}
	if err := ValidateArtifactTitle(strings.Repeat("a", MaxArtifactTitleLen+1)); err == nil {
		t.Fatal("an over-long title must be rejected")
	}
	if err := ValidateArtifactTitle("Agent usage by day"); err != nil {
		t.Fatalf("a normal title was rejected: %v", err)
	}
}

func TestArtifactContentSizeCap(t *testing.T) {
	if err := ValidateArtifactContent(ArtifactKindMarkdown, ""); err == nil {
		t.Fatal("empty content must be rejected")
	}
	// Exactly at the cap is fine; one byte over is not.
	atCap := strings.Repeat("x", MaxArtifactContentBytes)
	if err := ValidateArtifactContent(ArtifactKindMarkdown, atCap); err != nil {
		t.Fatalf("content exactly at the cap was rejected: %v", err)
	}
	err := ValidateArtifactContent(ArtifactKindMarkdown, atCap+"x")
	if err == nil {
		t.Fatal("content over the cap must be rejected")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("size error = %q, want it to say the content is too large", err.Error())
	}
}

func TestMarkdownAndHTMLTakeArbitraryBodies(t *testing.T) {
	// html is NOT parsed or sanitized here on purpose: it only ever renders
	// inside the sandboxed iframe (plan §7.1), so there is nothing this layer
	// could check that would make it safer.
	for _, kind := range []string{ArtifactKindMarkdown, ArtifactKindHTML} {
		if err := ValidateArtifactContent(kind, "<script>alert(1)</script> not json at all"); err != nil {
			t.Fatalf("%s content was rejected: %v", kind, err)
		}
	}
}

func TestValidChartSpecRoundTrips(t *testing.T) {
	spec := `{
      "type": "bar",
      "x": "day",
      "series": [{"key": "runs", "label": "Runs"}, {"key": "failures", "label": "Failures"}],
      "rows": [{"day": "Mon", "runs": 12, "failures": 1}, {"day": "Tue", "runs": 9, "failures": 0}]
    }`
	if err := ValidateArtifact(ArtifactKindChart, "Runs by day", spec); err != nil {
		t.Fatalf("a well-formed chart spec was rejected: %v", err)
	}
	// Every chart type the native renderer supports.
	for _, typ := range []string{ChartTypeBar, ChartTypeLine, ChartTypeArea, ChartTypePie} {
		body := `{"type":"` + typ + `","x":"day","series":[{"key":"runs"}],"rows":[]}`
		if err := ValidateArtifactContent(ArtifactKindChart, body); err != nil {
			t.Fatalf("chart type %q was rejected: %v", typ, err)
		}
	}
}

func TestMalformedChartSpecsNameTheirField(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wants   string
	}{
		{"not json", `{"type": "bar",`, "valid JSON"},
		{"not an object", `[1,2,3]`, "JSON object"},
		{"unknown type", `{"type":"donut","x":"day","series":[{"key":"runs"}],"rows":[]}`, `"type"`},
		{"missing type", `{"x":"day","series":[{"key":"runs"}],"rows":[]}`, `"type"`},
		{"missing x", `{"type":"bar","series":[{"key":"runs"}],"rows":[]}`, `"x"`},
		{"blank x", `{"type":"bar","x":"  ","series":[{"key":"runs"}],"rows":[]}`, `"x"`},
		{"x not a string", `{"type":"bar","x":7,"series":[{"key":"runs"}],"rows":[]}`, `"x"`},
		{"series missing", `{"type":"bar","x":"day","rows":[]}`, `"series"`},
		{"series empty", `{"type":"bar","x":"day","series":[],"rows":[]}`, `"series"`},
		{"series not objects", `{"type":"bar","x":"day","series":["runs"],"rows":[]}`, "series[0]"},
		{"series key missing", `{"type":"bar","x":"day","series":[{"label":"Runs"}],"rows":[]}`, "series[0].key"},
		{"second series key blank", `{"type":"bar","x":"day","series":[{"key":"a"},{"key":""}],"rows":[]}`, "series[1].key"},
		{"rows missing", `{"type":"bar","x":"day","series":[{"key":"runs"}]}`, `"rows"`},
		{"rows not an array", `{"type":"bar","x":"day","series":[{"key":"runs"}],"rows":{}}`, `"rows"`},
		{"row not an object", `{"type":"bar","x":"day","series":[{"key":"runs"}],"rows":[{"day":"Mon"},["Tue",1]]}`, "rows[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArtifactContent(ArtifactKindChart, tc.content)
			if err == nil {
				t.Fatalf("accepted a malformed chart spec: %s", tc.content)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error = %q, want it to name %s", err.Error(), tc.wants)
			}
		})
	}
}

func TestValidTableSpecRoundTrips(t *testing.T) {
	spec := `{"columns":["Agent","Runs","Cost"],"rows":[["claude",12,3.4],["gpt",4,1.1]]}`
	if err := ValidateArtifact(ArtifactKindTable, "Agent cost", spec); err != nil {
		t.Fatalf("a well-formed table spec was rejected: %v", err)
	}
	// A header with no rows yet is a legitimate "nothing in this window".
	if err := ValidateArtifactContent(ArtifactKindTable, `{"columns":["A"],"rows":[]}`); err != nil {
		t.Fatalf("an empty table was rejected: %v", err)
	}
}

func TestMalformedTableSpecsNameTheirField(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wants   string
	}{
		{"not json", `{"columns":`, "valid JSON"},
		{"not an object", `"a string"`, "JSON object"},
		{"columns missing", `{"rows":[]}`, `"columns"`},
		{"columns empty", `{"columns":[],"rows":[]}`, `"columns"`},
		{"column not a string", `{"columns":["A",7],"rows":[]}`, "columns[1]"},
		{"rows missing", `{"columns":["A"]}`, `"rows"`},
		{"rows not an array", `{"columns":["A"],"rows":{"a":1}}`, `"rows"`},
		{"row not an array", `{"columns":["A"],"rows":[["a"],{"A":"b"}]}`, "rows[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateArtifactContent(ArtifactKindTable, tc.content)
			if err == nil {
				t.Fatalf("accepted a malformed table spec: %s", tc.content)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("error = %q, want it to name %s", err.Error(), tc.wants)
			}
		})
	}
}

// ValidateArtifact is what the create path calls, so it has to reject on every
// axis — not just the spec shape.
func TestValidateArtifactChecksKindTitleAndContent(t *testing.T) {
	if err := ValidateArtifact("svg", "Title", "body"); err == nil {
		t.Fatal("an unknown kind must be rejected")
	}
	if err := ValidateArtifact(ArtifactKindMarkdown, "", "body"); err == nil {
		t.Fatal("a blank title must be rejected")
	}
	if err := ValidateArtifact(ArtifactKindChart, "Title", `{"type":"donut"}`); err == nil {
		t.Fatal("a malformed chart spec must be rejected")
	}
	if err := ValidateArtifact(ArtifactKindMarkdown, "Sprint report", "# Sprint report\n\nAll green."); err != nil {
		t.Fatalf("a valid markdown artifact was rejected: %v", err)
	}
}

// The artifact tools are part of the prompt's standing behaviour, not an
// optional extra: without this guidance the model answers "chart me X" with an
// ASCII table and never calls the tool at all.
func TestSystemPromptSteersTowardArtifacts(t *testing.T) {
	prompt := buildSystemPrompt(UserContext{Name: "Jamshid"}, "")
	for _, want := range []string{
		"create_artifact",
		"update_artifact",
		"VISUALIZATION",
		"GROUND IT FIRST",
		ArtifactKindChart,
		ArtifactKindTable,
		ArtifactKindMarkdown,
		ArtifactKindHTML,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt does not mention %q", want)
		}
	}
}
