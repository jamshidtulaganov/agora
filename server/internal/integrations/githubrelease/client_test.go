package githubrelease

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateRelease: POSTs to /repos/{owner}/{repo}/releases with a Bearer PAT
// and the tag/name/body payload.
func TestCreateRelease(t *testing.T) {
	type captured struct {
		method string
		path   string
		auth   string
		accept string
		body   map[string]any
	}
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		got <- captured{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept"), body}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	orig := APIBase
	APIBase = srv.URL
	defer func() { APIBase = orig }()

	err := NewClient().CreateRelease(context.Background(), "octocat", "hello-world", "ghp_tok", Release{
		TagName: "release-sprint-9",
		Name:    "Acme · Sprint 9",
		Body:    "- MUL-1 — fix\n",
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	c := <-got
	if c.method != http.MethodPost {
		t.Errorf("method = %s, want POST", c.method)
	}
	if c.path != "/repos/octocat/hello-world/releases" {
		t.Errorf("path = %s, want /repos/octocat/hello-world/releases", c.path)
	}
	if c.auth != "Bearer ghp_tok" {
		t.Errorf("auth = %q, want Bearer ghp_tok", c.auth)
	}
	if c.accept != "application/vnd.github+json" {
		t.Errorf("accept = %q, want application/vnd.github+json", c.accept)
	}
	if c.body["tag_name"] != "release-sprint-9" || c.body["name"] != "Acme · Sprint 9" {
		t.Errorf("unexpected body: %v", c.body)
	}
}

// TestCreateReleaseNon2xxIsError: a 422 (e.g. tag exists) surfaces an error.
func TestCreateReleaseNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	orig := APIBase
	APIBase = srv.URL
	defer func() { APIBase = orig }()
	if err := NewClient().CreateRelease(context.Background(), "o", "r", "t", Release{TagName: "v1"}); err == nil {
		t.Fatal("expected an error on a 422 response")
	}
}

// TestValidateToken: GETs the repo and returns the receiver's status; a closed
// server reports reachable=false.
func TestValidateToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/o/r" && r.Header.Get("Authorization") == "Bearer good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	orig := APIBase
	APIBase = srv.URL
	defer func() { APIBase = orig }()

	if status, reachable := NewClient().ValidateToken(context.Background(), "o", "r", "good"); status != http.StatusOK || !reachable {
		t.Errorf("valid token = (%d,%v), want (200,true)", status, reachable)
	}
	if status, reachable := NewClient().ValidateToken(context.Background(), "o", "r", "bad"); status != http.StatusUnauthorized || !reachable {
		t.Errorf("bad token = (%d,%v), want (401,true)", status, reachable)
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	APIBase = deadURL
	if _, reachable := NewClient().ValidateToken(context.Background(), "o", "r", "good"); reachable {
		t.Error("a closed server must report reachable=false")
	}
}
