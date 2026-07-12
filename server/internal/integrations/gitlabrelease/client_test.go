package gitlabrelease

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBaseURL: a bare host defaults to https; an explicit scheme is honored.
func TestBaseURL(t *testing.T) {
	cases := map[string]string{
		"gitlab.com":                 "https://gitlab.com",
		"gitlab.example.com/":        "https://gitlab.example.com",
		"https://gitlab.example.com": "https://gitlab.example.com",
		"http://127.0.0.1:1234":      "http://127.0.0.1:1234",
		"":                           "https://gitlab.com",
	}
	for in, want := range cases {
		if got := baseURL(in); got != want {
			t.Errorf("baseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCreateRelease: POSTs to /api/v4/projects/{url-encoded path}/releases with
// the PRIVATE-TOKEN header and the release payload.
func TestCreateRelease(t *testing.T) {
	type captured struct {
		method string
		path   string
		token  string
		body   map[string]any
	}
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		got <- captured{r.Method, r.URL.EscapedPath(), r.Header.Get("PRIVATE-TOKEN"), body}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	// host = the http test server URL (an explicit scheme, so no forced https).
	err := NewClient().CreateRelease(context.Background(), srv.URL, "group/repo", "glpat", Release{
		TagName:     "release-sprint-9",
		Name:        "Sprint 9",
		Description: "- MUL-1 — fix\n",
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	c := <-got
	if c.method != http.MethodPost {
		t.Errorf("method = %s, want POST", c.method)
	}
	// group/repo must be url-encoded to group%2Frepo.
	if !strings.HasPrefix(c.path, "/api/v4/projects/group%2Frepo/releases") {
		t.Errorf("path = %s, want /api/v4/projects/group%%2Frepo/releases", c.path)
	}
	if c.token != "glpat" {
		t.Errorf("PRIVATE-TOKEN = %q, want glpat", c.token)
	}
	if c.body["tag_name"] != "release-sprint-9" || c.body["description"] != "- MUL-1 — fix\n" {
		t.Errorf("unexpected body: %v", c.body)
	}
}

// TestValidateToken: GET on the project returns the receiver's status.
func TestValidateToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.Header.Get("PRIVATE-TOKEN") == "good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if status, reachable := NewClient().ValidateToken(context.Background(), srv.URL, "group/repo", "good"); status != http.StatusOK || !reachable {
		t.Errorf("valid token = (%d,%v), want (200,true)", status, reachable)
	}
	if status, reachable := NewClient().ValidateToken(context.Background(), srv.URL, "group/repo", "bad"); status != http.StatusUnauthorized || !reachable {
		t.Errorf("bad token = (%d,%v), want (401,true)", status, reachable)
	}
}
