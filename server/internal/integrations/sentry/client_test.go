package sentry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestCreateReleaseAndDeploy: creates the release then the deploy, hitting the
// two org-scoped endpoints with a Bearer token and the right bodies.
func TestCreateReleaseAndDeploy(t *testing.T) {
	type hit struct {
		path string
		auth string
		body map[string]any
	}
	var mu sync.Mutex
	var hits []hit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		hits = append(hits, hit{r.URL.Path, r.Header.Get("Authorization"), body})
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	err := NewClient().CreateReleaseAndDeploy(context.Background(), srv.URL, "acme", "backend", "sntrytok", "release-sprint-9", "production")
	if err != nil {
		t.Fatalf("CreateReleaseAndDeploy: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 2 {
		t.Fatalf("expected 2 requests (release + deploy), got %d", len(hits))
	}
	if hits[0].path != "/api/0/organizations/acme/releases/" {
		t.Errorf("release path = %s", hits[0].path)
	}
	if hits[0].auth != "Bearer sntrytok" {
		t.Errorf("release auth = %q, want Bearer sntrytok", hits[0].auth)
	}
	if hits[0].body["version"] != "release-sprint-9" {
		t.Errorf("release version = %v, want release-sprint-9", hits[0].body["version"])
	}
	if projects, ok := hits[0].body["projects"].([]any); !ok || len(projects) != 1 || projects[0] != "backend" {
		t.Errorf("release projects = %v, want [backend]", hits[0].body["projects"])
	}
	if hits[1].path != "/api/0/organizations/acme/releases/release-sprint-9/deploys/" {
		t.Errorf("deploy path = %s", hits[1].path)
	}
	if hits[1].body["environment"] != "production" {
		t.Errorf("deploy environment = %v, want production", hits[1].body["environment"])
	}
}

// TestCreateReleaseSkipsDeployWithoutEnv: an empty environment still creates the
// release but registers no deploy.
func TestCreateReleaseSkipsDeployWithoutEnv(t *testing.T) {
	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	if err := NewClient().CreateReleaseAndDeploy(context.Background(), srv.URL, "acme", "backend", "t", "release-x", ""); err != nil {
		t.Fatalf("CreateReleaseAndDeploy: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("expected only the release POST (no deploy), got %d requests", count)
	}
}

// TestValidateToken: GET on the org returns the receiver's status.
func TestValidateToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/0/organizations/acme/" && r.Header.Get("Authorization") == "Bearer good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	if status, reachable := NewClient().ValidateToken(context.Background(), srv.URL, "acme", "good"); status != http.StatusOK || !reachable {
		t.Errorf("valid token = (%d,%v), want (200,true)", status, reachable)
	}
	if status, reachable := NewClient().ValidateToken(context.Background(), srv.URL, "acme", "bad"); status != http.StatusForbidden || !reachable {
		t.Errorf("bad token = (%d,%v), want (403,true)", status, reachable)
	}
}

// TestNormalizeBaseURL: default + trailing slash handling.
func TestNormalizeBaseURL(t *testing.T) {
	if got := normalizeBaseURL(""); got != DefaultBaseURL {
		t.Errorf("empty base = %q, want %q", got, DefaultBaseURL)
	}
	if got := normalizeBaseURL("https://sentry.example.com/"); got != "https://sentry.example.com" {
		t.Errorf("trailing slash not trimmed: %q", got)
	}
}
