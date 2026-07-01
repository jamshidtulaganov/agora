package handler

import "testing"

func TestCoCodeBranchNameStress(t *testing.T) {
	cases := []struct {
		name       string
		issueKey   string
		issueTitle string
		want       string
	}{
		{
			name:       "typical_key_and_title",
			issueKey:   "SD-42",
			issueTitle: "Add widget endpoint",
			want:       "cocode/sd-42-add-widget-endpoint",
		},
		{
			name:       "key_with_special_chars_in_title",
			issueKey:   "SD-100",
			issueTitle: "fix: null pointer in /api/v2",
			want:       "cocode/sd-100-fix-null-pointer-in-api-v2",
		},
		{
			name:       "empty_title",
			issueKey:   "MUL-7",
			issueTitle: "",
			want:       "cocode/mul-7",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := coCodeBranchName(c.issueKey, c.issueTitle); got != c.want {
				t.Errorf("coCodeBranchName(%q, %q) = %q, want %q", c.issueKey, c.issueTitle, got, c.want)
			}
		})
	}
}
