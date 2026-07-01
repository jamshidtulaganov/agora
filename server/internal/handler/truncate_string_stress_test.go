package handler

import "testing"

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
		want  string
	}{
		{
			name:  "shorter than limit",
			input: "hello",
			max:   10,
			want:  "hello",
		},
		{
			name:  "longer than limit",
			input: "hello world",
			max:   5,
			want:  "hello",
		},
		{
			name:  "exactly at limit",
			input: "hello",
			max:   5,
			want:  "hello",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateString(tc.input, tc.max)
			if got != tc.want {
				t.Errorf("truncateString(%q, %d) = %q; want %q", tc.input, tc.max, got, tc.want)
			}
		})
	}
}
