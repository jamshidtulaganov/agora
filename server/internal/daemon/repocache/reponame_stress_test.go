package repocache

import "testing"

func TestRepoNameFromURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "https github url",
			input: "https://github.com/org/my-repo",
			want:  "my-repo",
		},
		{
			name:  "ssh git url",
			input: "git@github.com:org/my-repo.git",
			want:  "my-repo",
		},
		{
			name:  "https url with trailing .git",
			input: "https://github.com/org/my-repo.git",
			want:  "my-repo",
		},
		{
			name:  "bare name",
			input: "my-repo",
			want:  "my-repo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repoNameFromURL(tc.input)
			if got != tc.want {
				t.Errorf("repoNameFromURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
