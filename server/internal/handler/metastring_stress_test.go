package handler

import (
	"encoding/json"
	"testing"
)

func TestMetaString(t *testing.T) {
	marshal := func(v map[string]any) []byte {
		b, _ := json.Marshal(v)
		return b
	}

	tests := []struct {
		name string
		raw  []byte
		key  string
		want string
	}{
		{
			name: "key present as string",
			raw:  marshal(map[string]any{"status": "open"}),
			key:  "status",
			want: "open",
		},
		{
			name: "key missing",
			raw:  marshal(map[string]any{"other": "value"}),
			key:  "status",
			want: "",
		},
		{
			name: "non-string value",
			raw:  marshal(map[string]any{"count": 42}),
			key:  "count",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := metaString(tc.raw, tc.key)
			if got != tc.want {
				t.Errorf("metaString(%q, %q) = %q; want %q", tc.raw, tc.key, got, tc.want)
			}
		})
	}
}
