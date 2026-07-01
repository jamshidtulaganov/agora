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
			name: "non-string value number",
			raw:  marshal(map[string]any{"count": 42}),
			key:  "count",
			want: "",
		},
		{
			name: "key present as boolean",
			raw:  marshal(map[string]any{"active": true}),
			key:  "active",
			want: "",
		},
		{
			name: "key present as null",
			raw:  marshal(map[string]any{"status": nil}),
			key:  "status",
			want: "",
		},
		{
			name: "key present as nested object",
			raw:  marshal(map[string]any{"meta": map[string]any{"foo": "bar"}}),
			key:  "meta",
			want: "",
		},
		{
			name: "empty byte slice",
			raw:  []byte{},
			key:  "status",
			want: "",
		},
		{
			name: "nil bytes",
			raw:  nil,
			key:  "status",
			want: "",
		},
		{
			name: "invalid JSON",
			raw:  []byte(`{not valid json`),
			key:  "status",
			want: "",
		},
		{
			name: "empty string value for present key",
			raw:  marshal(map[string]any{"status": ""}),
			key:  "status",
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
