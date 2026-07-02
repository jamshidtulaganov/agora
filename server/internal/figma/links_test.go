package figma

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRefsFrom(t *testing.T) {
	// The real MUL-348 URL this feature exists for.
	mul348 := "https://www.figma.com/design/cF4PFq3P5NOyZvp01JSHnE/Sales-Doctor-Dashboard?node-id=208-5147&p=f&t=5N85gGmuiIY1odti-0"

	tests := []struct {
		name string
		text string
		want []Ref
	}{
		{"empty", "", nil},
		{"no figma", "see https://example.com/design/abc", nil},
		{
			"mul-348 design url",
			"**Описание:** Создание интерфейсных компонентов на основе макетов в Figma. " + mul348,
			[]Ref{{URL: mul348, FileKey: "cF4PFq3P5NOyZvp01JSHnE", NodeID: "208:5147"}},
		},
		{
			"file url without node id",
			"https://www.figma.com/file/AbCdEf123456/My-File",
			[]Ref{{URL: "https://www.figma.com/file/AbCdEf123456/My-File", FileKey: "AbCdEf123456", NodeID: ""}},
		},
		{
			"proto url, no www",
			"https://figma.com/proto/AbCdEf123456/Flow?node-id=1-2",
			[]Ref{{URL: "https://figma.com/proto/AbCdEf123456/Flow?node-id=1-2", FileKey: "AbCdEf123456", NodeID: "1:2"}},
		},
		{
			"percent-encoded node id",
			"https://www.figma.com/design/AbCdEf123456/X?node-id=208%3A5147",
			[]Ref{{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=208%3A5147", FileKey: "AbCdEf123456", NodeID: "208:5147"}},
		},
		{
			"markdown link trailing paren excluded",
			"see [the design](https://www.figma.com/design/AbCdEf123456/X?node-id=3-4) for details",
			[]Ref{{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=3-4", FileKey: "AbCdEf123456", NodeID: "3:4"}},
		},
		{
			"two distinct nodes in one file",
			"https://www.figma.com/design/AbCdEf123456/X?node-id=1-1 and https://www.figma.com/design/AbCdEf123456/X?node-id=2-2",
			[]Ref{
				{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=1-1", FileKey: "AbCdEf123456", NodeID: "1:1"},
				{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=2-2", FileKey: "AbCdEf123456", NodeID: "2:2"},
			},
		},
		{
			"duplicate (file, node) deduped",
			"https://www.figma.com/design/AbCdEf123456/X?node-id=1-1\nhttps://www.figma.com/design/AbCdEf123456/Y?node-id=1-1",
			[]Ref{{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=1-1", FileKey: "AbCdEf123456", NodeID: "1:1"}},
		},
		{
			"short file key rejected",
			"https://www.figma.com/design/short/X?node-id=1-1",
			nil,
		},
		{
			"board url not matched",
			"https://www.figma.com/board/AbCdEf123456/Whiteboard",
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RefsFrom(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d refs %+v, want %d", len(got), got, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ref[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestLinksMetadataValueRoundTrip(t *testing.T) {
	refs := []Ref{
		{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=1-1", FileKey: "AbCdEf123456", NodeID: "1:1"},
	}
	v := LinksMetadataValue(refs)
	if v == "" {
		t.Fatal("expected non-empty stamp")
	}
	// The stamp must be a primitive JSON string when marshaled as a metadata
	// value (V1 contract: no arrays/objects).
	value, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var scalar any
	if err := json.Unmarshal(value, &scalar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := scalar.(string); !ok {
		t.Fatalf("metadata value must be a JSON string, got %T", scalar)
	}
	back := ParseLinksMetadataValue(v)
	if len(back) != 1 || back[0] != refs[0] {
		t.Fatalf("round-trip = %+v, want %+v", back, refs)
	}
}

func TestLinksMetadataValueCaps(t *testing.T) {
	if LinksMetadataValue(nil) != "" {
		t.Error("no refs must produce no stamp")
	}
	// 7 links → capped at 5.
	var refs []Ref
	for i := 0; i < 7; i++ {
		refs = append(refs, Ref{
			URL:     "https://www.figma.com/design/AbCdEf12345" + string(rune('0'+i)) + "/X",
			FileKey: "AbCdEf12345" + string(rune('0'+i)),
		})
	}
	back := ParseLinksMetadataValue(LinksMetadataValue(refs))
	if len(back) != 5 {
		t.Fatalf("got %d stamped refs, want cap of 5", len(back))
	}
	// A pathologically long URL set must skip the stamp instead of risking
	// the 8KB metadata CHECK.
	huge := []Ref{{URL: "https://www.figma.com/design/AbCdEf123456/" + strings.Repeat("x", 5000), FileKey: "AbCdEf123456"}}
	if LinksMetadataValue(huge) != "" {
		t.Error("oversized stamp must be skipped")
	}
}

func TestParseLinksMetadataValueMalformed(t *testing.T) {
	if ParseLinksMetadataValue("") != nil {
		t.Error("empty → nil")
	}
	if ParseLinksMetadataValue("not json") != nil {
		t.Error("malformed → nil")
	}
	if ParseLinksMetadataValue(`{"url":"x"}`) != nil {
		t.Error("non-array → nil")
	}
}
