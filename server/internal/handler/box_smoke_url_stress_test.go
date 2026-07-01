package handler

import (
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestBoxSmokeURL covers every branch of boxSmokeURL:
//
//   - empty/whitespace/slash-only work_dir → "" (the wd=="" early return)
//   - normal /var/www/<name> path → "https://<name>"
//   - trailing slash is stripped before extraction
//   - no slash in work_dir → the whole value becomes the subdomain
//
// The sub=="" branch (second early return) is unreachable in practice:
// TrimRight removes all trailing '/' so wd never ends with '/', which means
// LastIndex can never return len(wd)-1, which means sub is always non-empty
// when wd is non-empty. It is defensive code; we note it here rather than
// contrive an impossible input.
func TestBoxSmokeURL(t *testing.T) {
	tests := []struct {
		name    string
		workDir string
		want    string
	}{
		// wd == "" branch
		{
			name:    "empty work_dir returns empty",
			workDir: "",
			want:    "",
		},
		{
			name:    "whitespace-only work_dir returns empty",
			workDir: "   ",
			want:    "",
		},
		{
			name:    "single slash returns empty",
			workDir: "/",
			want:    "",
		},
		{
			name:    "multiple slashes return empty",
			workDir: "///",
			want:    "",
		},
		{
			name:    "slash with surrounding spaces returns empty",
			workDir: "  /  ",
			want:    "",
		},

		// happy path: "https://" + last path segment
		{
			name:    "standard /var/www/<name> path",
			workDir: "/var/www/myapp",
			want:    "https://myapp",
		},
		{
			name:    "trailing slash is stripped before extraction",
			workDir: "/var/www/myapp/",
			want:    "https://myapp",
		},
		{
			name:    "multiple trailing slashes are stripped",
			workDir: "/var/www/myapp///",
			want:    "https://myapp",
		},
		{
			name:    "subdomain-style name with dots",
			workDir: "/var/www/alice.qa.example.com",
			want:    "https://alice.qa.example.com",
		},
		{
			name:    "no slash in work_dir uses the whole value as subdomain",
			workDir: "myapp",
			want:    "https://myapp",
		},
		{
			name:    "single leading slash only yields the segment",
			workDir: "/myapp",
			want:    "https://myapp",
		},
		{
			name:    "deeply nested path extracts last segment only",
			workDir: "/a/b/c/d/e/name",
			want:    "https://name",
		},
		{
			name:    "leading and trailing spaces are trimmed before extraction",
			workDir: "  /var/www/trimmed  ",
			want:    "https://trimmed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			box := db.ConnectedBox{WorkDir: tc.workDir}
			got := boxSmokeURL(box)
			if got != tc.want {
				t.Fatalf("boxSmokeURL(%q) = %q, want %q", tc.workDir, got, tc.want)
			}
		})
	}
}
