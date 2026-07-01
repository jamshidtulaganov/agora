package handler

import "testing"

func TestSanitizeMentionLabel(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain name",
			input: "Alice",
			want:  "Alice",
		},
		{
			name:  "name with brackets and parens",
			input: "[Bob] (QA)",
			want:  "[Bob (QA)",
		},
		{
			name:  "empty string",
			input: "",
			want:  "assignee",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeMentionLabel(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeMentionLabel(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
