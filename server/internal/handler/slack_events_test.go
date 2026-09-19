package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/slack"
)

const slackEventsSigningSecret = "events-signing-secret"

// signedSlackEvent builds a request Slack would have sent: raw JSON body plus
// the two signing headers, computed over those exact bytes.
func signedSlackEvent(body string, ts time.Time) *http.Request {
	stamp := strconv.FormatInt(ts.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(slack.HeaderTimestamp, stamp)
	req.Header.Set(slack.HeaderSignature, slack.ComputeSignature(slackEventsSigningSecret, stamp, []byte(body)))
	return req
}

func TestSlackEvents_URLVerificationChallenge(t *testing.T) {
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)

	body := `{"type":"url_verification","token":"legacy","challenge":"3eZbrw1aB1caB1caB1"}`
	rec := httptest.NewRecorder()
	testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now()))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "3eZbrw1aB1caB1caB1" {
		t.Fatalf("challenge echo = %q", got)
	}
}

func TestSlackEvents_SignatureRejections(t *testing.T) {
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	body := `{"type":"url_verification","challenge":"abc"}`

	t.Run("tampered body", func(t *testing.T) {
		req := signedSlackEvent(body, time.Now())
		// Same headers, different bytes — what a MITM rewrite looks like.
		req.Body = http.NoBody
		tampered := httptest.NewRequest(http.MethodPost, "/slack/events",
			strings.NewReader(`{"type":"url_verification","challenge":"attacker"}`))
		tampered.Header = req.Header
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, tampered)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("stale timestamp", func(t *testing.T) {
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now().Add(-10*time.Minute)))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("no signature headers", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/slack/events", strings.NewReader(body))
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("wrong signing secret", func(t *testing.T) {
		req := signedSlackEvent(body, time.Now())
		t.Setenv("AGORA_SLACK_SIGNING_SECRET", "a-different-secret")
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})
}

// Degraded mode: no signing secret means we cannot tell a real delivery from a
// forged one, so the endpoint fails closed instead of trusting the payload.
func TestSlackEvents_NotConfiguredReturns503(t *testing.T) {
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", "")
	rec := httptest.NewRecorder()
	testHandler.SlackEvents(rec, httptest.NewRequest(http.MethodPost, "/slack/events",
		strings.NewReader(`{"type":"url_verification","challenge":"abc"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestSlackEvents_UnhandledEventIsAcked(t *testing.T) {
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	// PR 1 subscribes to nothing but the lifecycle events; anything else must
	// be acknowledged, because a non-2xx buys retries and, at volume, a
	// disabled subscription.
	body := `{"type":"event_callback","team_id":"T0TESTTEAM","event":{"type":"link_shared"}}`
	rec := httptest.NewRecorder()
	testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now()))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestSlackEvents_MalformedPayload(t *testing.T) {
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	rec := httptest.NewRecorder()
	testHandler.SlackEvents(rec, signedSlackEvent(`{"type":`, time.Now()))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSlackEvents_AppIDMismatchRejected(t *testing.T) {
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	t.Setenv("AGORA_SLACK_APP_ID", "A0OURS")
	body := `{"type":"event_callback","api_app_id":"A0SOMEONEELSE","team_id":"T0TESTTEAM","event":{"type":"link_shared"}}`
	rec := httptest.NewRecorder()
	testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now()))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// app_uninstalled / tokens_revoked arrive with a team id and no workspace, and
// one Slack team may back several Agora workspaces — so revocation is
// team-wide, and it marks rather than deletes.
func TestSlackEvents_LifecycleEventsRevokeInstallations(t *testing.T) {
	configureSlack(t)
	t.Setenv("AGORA_SLACK_SIGNING_SECRET", slackEventsSigningSecret)
	cleanupSlackRows(t)
	ctx := context.Background()

	seed := func(t *testing.T) string {
		t.Helper()
		var id string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO slack_installation (
				workspace_id, team_id, team_name, app_id, bot_user_id,
				bot_token_encrypted, scopes, installer_user_id
			) VALUES ($1::uuid, $2, 'Agora Test', 'A0APP', 'U0BOT', '\x00'::bytea, 'chat:write', $3::uuid)
			ON CONFLICT (workspace_id, team_id) DO UPDATE SET status = 'active'
			RETURNING id::text
		`, testWorkspaceID, slackTestTeamID, testUserID).Scan(&id); err != nil {
			t.Fatalf("seed installation: %v", err)
		}
		return id
	}
	statusOf := func(t *testing.T, id string) string {
		t.Helper()
		var status string
		if err := testPool.QueryRow(ctx, `SELECT status FROM slack_installation WHERE id = $1::uuid`, id).Scan(&status); err != nil {
			t.Fatalf("status: %v", err)
		}
		return status
	}

	t.Run("app_uninstalled", func(t *testing.T) {
		id := seed(t)
		body := `{"type":"event_callback","team_id":"` + slackTestTeamID + `","event":{"type":"app_uninstalled"}}`
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now()))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if got := statusOf(t, id); got != "revoked" {
			t.Fatalf("status = %q, want revoked", got)
		}
	})

	t.Run("tokens_revoked with a bot token", func(t *testing.T) {
		id := seed(t)
		body := `{"type":"event_callback","team_id":"` + slackTestTeamID + `","event":{"type":"tokens_revoked","tokens":{"bot":["U0BOT"]}}}`
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now()))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if got := statusOf(t, id); got != "revoked" {
			t.Fatalf("status = %q, want revoked", got)
		}
	})

	t.Run("tokens_revoked with only user tokens leaves the install alone", func(t *testing.T) {
		id := seed(t)
		// A person disconnecting their personal Slack link must not take the
		// whole workspace's notifications down with them.
		body := `{"type":"event_callback","team_id":"` + slackTestTeamID + `","event":{"type":"tokens_revoked","tokens":{"oauth":["U0HUMAN"]}}}`
		rec := httptest.NewRecorder()
		testHandler.SlackEvents(rec, signedSlackEvent(body, time.Now()))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if got := statusOf(t, id); got != "active" {
			t.Fatalf("status = %q, want active", got)
		}
	})
}
