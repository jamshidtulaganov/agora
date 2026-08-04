package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/githubrelease"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// shippedPayload is a representative release:shipped enriched payload.
func shippedPayload() map[string]any {
	return map[string]any{
		"workspace_id": "ws-1",
		"project":      "Acme",
		"sprint":       "Sprint 9",
		"branch":       "release/sprint-9",
		"environment":  "production",
		"issue_ids":    []string{"i1"},
	}
}

func sampleChangelog() []releaseChangelogEntry {
	return []releaseChangelogEntry{
		{ID: "i1", Identifier: "MUL-1", Title: "Fix login", Verdict: "pass", BitrixTaskID: "555"},
		{ID: "i2", Identifier: "MUL-2", Title: "Add export", Verdict: "pass"},
	}
}

// TestReleaseConnectorFor maps each known kind to a connector and unknown → nil.
func TestReleaseConnectorFor(t *testing.T) {
	for _, kind := range []string{"webhook", "slack", "bitrix", "github_release", "gitlab_release", "sentry"} {
		if releaseConnectorFor(kind) == nil {
			t.Errorf("releaseConnectorFor(%q) = nil, want a connector", kind)
		}
	}
	if releaseConnectorFor("future_kind") != nil {
		t.Error("an unknown kind must map to nil (enum-drift-downgrades)")
	}
}

// TestSlackConnectorPostsMessage: the slack connector posts a {text} message
// carrying the ship headline + changelog bullets.
func TestSlackConnectorPostsMessage(t *testing.T) {
	got := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got <- m
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := mustJSON(t, slackSecret{WebhookURL: srv.URL})
	err := releaseSlackConnector(context.Background(), nil, secret, protocol.EventReleaseShipped, shippedPayload(), sampleChangelog())
	if err != nil {
		t.Fatalf("slack connector: %v", err)
	}
	m := <-got
	text, _ := m["text"].(string)
	if text == "" {
		t.Fatal("empty slack text")
	}
	for _, want := range []string{"🚀", "Acme · Sprint 9", "production", "MUL-1 — Fix login", "MUL-2 — Add export"} {
		if !strings.Contains(text, want) {
			t.Errorf("slack text missing %q; got %q", want, text)
		}
	}
}

// TestSlackConnectorMissingSecret: no webhook_url → the connector errors (never
// posts).
func TestSlackConnectorMissingSecret(t *testing.T) {
	if err := releaseSlackConnector(context.Background(), nil, []byte(`{}`), protocol.EventReleaseShipped, shippedPayload(), nil); err == nil {
		t.Fatal("expected an error when webhook_url is missing")
	}
}

// TestGitHubConnectorCreatesRelease: POSTs a release with the sanitized tag +
// changelog markdown body + Bearer PAT.
func TestGitHubConnectorCreatesRelease(t *testing.T) {
	got := make(chan map[string]any, 1)
	auth := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		auth <- r.Header.Get("Authorization")
		got <- m
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	orig := githubrelease.APIBase
	githubrelease.APIBase = srv.URL
	defer func() { githubrelease.APIBase = orig }()

	cfg := mustJSON(t, map[string]string{"owner": "octocat", "repo": "hello"})
	secret := mustJSON(t, githubReleaseSecret{Token: "ghp_x"})
	if err := releaseGitHubConnector(context.Background(), cfg, secret, protocol.EventReleaseShipped, shippedPayload(), sampleChangelog()); err != nil {
		t.Fatalf("github connector: %v", err)
	}
	if a := <-auth; a != "Bearer ghp_x" {
		t.Errorf("auth = %q, want Bearer ghp_x", a)
	}
	m := <-got
	if m["tag_name"] != "release-sprint-9" {
		t.Errorf("tag_name = %v, want release-sprint-9", m["tag_name"])
	}
	body, _ := m["body"].(string)
	if !strings.Contains(body, "MUL-1 — Fix login") {
		t.Errorf("release body missing changelog: %q", body)
	}
}

// TestGitHubConnectorIgnoresDeployRecorded: the git connectors only act on
// release:shipped.
func TestGitHubConnectorIgnoresDeployRecorded(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	orig := githubrelease.APIBase
	githubrelease.APIBase = srv.URL
	defer func() { githubrelease.APIBase = orig }()
	cfg := mustJSON(t, map[string]string{"owner": "o", "repo": "r"})
	secret := mustJSON(t, githubReleaseSecret{Token: "t"})
	if err := releaseGitHubConnector(context.Background(), cfg, secret, protocol.EventDeployRecorded, map[string]any{}, nil); err != nil {
		t.Fatalf("github connector: %v", err)
	}
	if called {
		t.Error("github connector must not create a release on deploy:recorded")
	}
}

// TestGitLabConnectorCreatesRelease: PRIVATE-TOKEN + url-encoded project path.
func TestGitLabConnectorCreatesRelease(t *testing.T) {
	path := make(chan string, 1)
	token := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		path <- r.URL.EscapedPath()
		token <- r.Header.Get("PRIVATE-TOKEN")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := mustJSON(t, map[string]string{"project_path": "group/repo"})
	secret := mustJSON(t, gitlabReleaseSecret{Token: "glpat", Host: srv.URL})
	if err := releaseGitLabConnector(context.Background(), cfg, secret, protocol.EventReleaseShipped, shippedPayload(), sampleChangelog()); err != nil {
		t.Fatalf("gitlab connector: %v", err)
	}
	if p := <-path; p != "/api/v4/projects/group%2Frepo/releases" {
		t.Errorf("path = %s", p)
	}
	if tk := <-token; tk != "glpat" {
		t.Errorf("PRIVATE-TOKEN = %q, want glpat", tk)
	}
}

// TestSentryConnectorCreatesReleaseAndDeploy: two org POSTs (release + deploy).
func TestSentryConnectorCreatesReleaseAndDeploy(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := mustJSON(t, map[string]string{"org": "acme", "project": "backend"})
	secret := mustJSON(t, sentrySecret{Token: "sntry", BaseURL: srv.URL})
	if err := releaseSentryConnector(context.Background(), cfg, secret, protocol.EventReleaseShipped, shippedPayload(), nil); err != nil {
		t.Fatalf("sentry connector: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 {
		t.Fatalf("expected 2 requests (release + deploy), got %d: %v", len(paths), paths)
	}
	if paths[0] != "/api/0/organizations/acme/releases/" {
		t.Errorf("release path = %s", paths[0])
	}
	if paths[1] != "/api/0/organizations/acme/releases/release-sprint-9/deploys/" {
		t.Errorf("deploy path = %s", paths[1])
	}
}

// TestBitrixConnectorCommentsShippedTasks: comments on each changelog entry that
// carries a bitrix_task_id, via a per-workspace override portal.
func TestBitrixConnectorCommentsShippedTasks(t *testing.T) {
	type comment struct {
		taskID  string
		message string
	}
	got := make(chan comment, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got <- comment{taskID: r.FormValue("TASKID"), message: r.FormValue("FIELDS[POST_MESSAGE]")}
		_, _ = w.Write([]byte(`{"result":true}`))
	}))
	defer srv.Close()

	secret := mustJSON(t, bitrixReleaseSecret{WebhookURL: srv.URL + "/rest/1/tok/"})
	if err := releaseBitrixConnector(context.Background(), nil, secret, protocol.EventReleaseShipped, shippedPayload(), sampleChangelog()); err != nil {
		t.Fatalf("bitrix connector: %v", err)
	}
	// Only the first changelog entry carries a bitrix_task_id (555).
	c := <-got
	if c.taskID != "555" {
		t.Errorf("commented task id = %q, want 555", c.taskID)
	}
	if !strings.Contains(c.message, "shipped in Sprint 9 → production") {
		t.Errorf("comment = %q", c.message)
	}
	select {
	case extra := <-got:
		t.Errorf("expected exactly one comment, got a second for task %q", extra.taskID)
	default:
	}
}

// TestBitrixConnectorNoopWithoutPortal: no override secret AND no env portal →
// clean no-op (no panic, no error).
func TestBitrixConnectorNoopWithoutPortal(t *testing.T) {
	t.Setenv("BITRIX_WEBHOOK_URL", "")
	if err := releaseBitrixConnector(context.Background(), nil, nil, protocol.EventReleaseShipped, shippedPayload(), sampleChangelog()); err != nil {
		t.Errorf("expected a clean no-op, got %v", err)
	}
}

// TestWebhookConnectorDeliversSignedBody: the generic webhook still POSTs
// {event, ...payload, changelog}, signed (Phase 2 behavior preserved).
func TestWebhookConnectorDeliversSignedBody(t *testing.T) {
	got := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got <- m
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := mustJSON(t, webhookSecret{URL: srv.URL, Signing: "k"})
	if err := releaseWebhookConnector(context.Background(), nil, secret, protocol.EventReleaseShipped, shippedPayload(), sampleChangelog()); err != nil {
		t.Fatalf("webhook connector: %v", err)
	}
	m := <-got
	if m["event"] != protocol.EventReleaseShipped {
		t.Errorf("event = %v, want %s", m["event"], protocol.EventReleaseShipped)
	}
	if m["environment"] != "production" {
		t.Errorf("payload field missing: %v", m)
	}
	if _, ok := m["changelog"].([]any); !ok {
		t.Errorf("changelog missing from body: %v", m["changelog"])
	}
}

// TestSanitizeReleaseTag: strips an existing release prefix, slugs the rest, and
// never double-prefixes or yields an empty tag.
func TestSanitizeReleaseTag(t *testing.T) {
	cases := map[string]string{
		"Sprint 9":         "release-sprint-9",
		"release/sprint-9": "release-sprint-9",
		"release-Sprint 9": "release-sprint-9",
		"  Q4 // Ship!!  ": "release-q4-ship",
		"":                 "release-release",
	}
	for in, want := range cases {
		if got := sanitizeReleaseTag(in); got != want {
			t.Errorf("sanitizeReleaseTag(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSlackReleaseTextDeploy: deploy:recorded renders "Deploy to X: status".
func TestSlackReleaseTextDeploy(t *testing.T) {
	text := slackReleaseText(protocol.EventDeployRecorded, map[string]any{"target": "qa-box", "status": "success"}, nil)
	if text != "Deploy to qa-box: success" {
		t.Errorf("deploy text = %q", text)
	}
}

// TestBuildReleaseConfig: required config fields are enforced per kind and a
// metadata edit keeps existing values.
func TestBuildReleaseConfig(t *testing.T) {
	// github_release missing repo → rejected.
	if _, _, ok := buildReleaseConfig("github_release", &releaseIntegrationRequest{Owner: "o"}, releaseConfigFields{}); ok {
		t.Error("github_release without repo must be rejected")
	}
	// github_release complete → accepted, config carries owner+repo.
	cfg, _, ok := buildReleaseConfig("github_release", &releaseIntegrationRequest{Owner: "o", Repo: "r", Name: "gh"}, releaseConfigFields{})
	if !ok {
		t.Fatal("complete github_release config rejected")
	}
	parsed := parseReleaseConfig(cfg)
	if parsed.Owner != "o" || parsed.Repo != "r" || parsed.Name != "gh" {
		t.Errorf("config = %+v", parsed)
	}
	// Update keeps existing owner when the request omits it.
	cfg2, _, ok := buildReleaseConfig("github_release", &releaseIntegrationRequest{Repo: "r2"}, releaseConfigFields{Owner: "o", Repo: "r"})
	if !ok {
		t.Fatal("merge update rejected")
	}
	if p := parseReleaseConfig(cfg2); p.Owner != "o" || p.Repo != "r2" {
		t.Errorf("merged config = %+v, want owner=o repo=r2", p)
	}
	// sentry / gitlab required fields.
	if _, _, ok := buildReleaseConfig("sentry", &releaseIntegrationRequest{Org: "acme"}, releaseConfigFields{}); ok {
		t.Error("sentry without project must be rejected")
	}
	if _, _, ok := buildReleaseConfig("gitlab_release", &releaseIntegrationRequest{}, releaseConfigFields{}); ok {
		t.Error("gitlab_release without project_path must be rejected")
	}
}

// TestBuildReleaseSecretPlain: validates + shapes the sealed secret per kind.
func TestBuildReleaseSecretPlain(t *testing.T) {
	// webhook: bad URL rejected.
	if _, _, ok := buildReleaseSecretPlain("webhook", &releaseIntegrationRequest{URL: "not-a-url"}); ok {
		t.Error("webhook with a bad URL must be rejected")
	}
	// slack: needs a valid webhook_url.
	if _, _, ok := buildReleaseSecretPlain("slack", &releaseIntegrationRequest{WebhookURL: "ftp://x"}); ok {
		t.Error("slack with a non-http webhook_url must be rejected")
	}
	// github: token required.
	if _, _, ok := buildReleaseSecretPlain("github_release", &releaseIntegrationRequest{}); ok {
		t.Error("github_release without a token must be rejected")
	}
	// gitlab: token present, host defaulted into the secret.
	plain, _, ok := buildReleaseSecretPlain("gitlab_release", &releaseIntegrationRequest{Token: "glpat", Host: "gitlab.example.com"})
	if !ok {
		t.Fatal("gitlab_release with a token rejected")
	}
	var gl gitlabReleaseSecret
	if json.Unmarshal(plain, &gl); gl.Token != "glpat" || gl.Host != "gitlab.example.com" {
		t.Errorf("gitlab secret = %+v", gl)
	}
	// sentry: base_url rides in the secret.
	plain, _, ok = buildReleaseSecretPlain("sentry", &releaseIntegrationRequest{Token: "t", BaseURL: "https://sentry.example.com"})
	if !ok {
		t.Fatal("sentry with a token rejected")
	}
	var sn sentrySecret
	if json.Unmarshal(plain, &sn); sn.Token != "t" || sn.BaseURL != "https://sentry.example.com" {
		t.Errorf("sentry secret = %+v", sn)
	}
}
