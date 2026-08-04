package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/releasehook"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

func TestReleaseEventShortName(t *testing.T) {
	cases := map[string]string{
		protocol.EventDeployRecorded: "deploy_recorded",
		protocol.EventReleaseShipped: "release_shipped",
		"issue:created":              "",
	}
	for in, want := range cases {
		if got := releaseEventShortName(in); got != want {
			t.Errorf("releaseEventShortName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReleaseIntegrationMatchesEvent(t *testing.T) {
	if !releaseIntegrationMatchesEvent([]string{"deploy_recorded"}, protocol.EventDeployRecorded) {
		t.Error("deploy_recorded filter must match deploy:recorded")
	}
	if releaseIntegrationMatchesEvent([]string{"release_shipped"}, protocol.EventDeployRecorded) {
		t.Error("release_shipped filter must NOT match deploy:recorded")
	}
	if releaseIntegrationMatchesEvent([]string{"deploy_recorded"}, "issue:created") {
		t.Error("a non-release event must never match")
	}
	if releaseIntegrationMatchesEvent(nil, protocol.EventDeployRecorded) {
		t.Error("an empty filter matches nothing")
	}
}

// hitRecorder is a webhook receiver that counts deliveries and keeps the last
// body + signature for verification.
type hitRecorder struct {
	hits atomic.Int32
	sig  atomic.Value // string
	body atomic.Value // []byte
}

func newHitServer(t *testing.T, rec *hitRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.body.Store(b)
		rec.sig.Store(r.Header.Get(releasehook.SignatureHeader))
		rec.hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFanOutReleaseEvent: the dispatcher delivers ONLY to enabled integrations
// whose events[] filter includes the fired event; a disabled row and an
// enabled-but-non-matching row receive nothing. The delivered body is signed.
func TestFanOutReleaseEvent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)
	box, err := releaseIntegrationBox()
	if err != nil {
		t.Fatalf("release box: %v", err)
	}

	wsID := createMcpTestWorkspace(t, ctx, "handler-tests-release-fanout", "owner")

	var matchRec, disabledRec, otherEventRec hitRecorder
	matchSrv := newHitServer(t, &matchRec)
	disabledSrv := newHitServer(t, &disabledRec)
	otherSrv := newHitServer(t, &otherEventRec)

	seal := func(url, signing string) []byte {
		sealed, serr := sealWebhookSecret(box, webhookSecret{URL: url, Signing: signing})
		if serr != nil {
			t.Fatalf("seal: %v", serr)
		}
		return sealed
	}
	insert := func(url, signing string, events []string, enabled bool) {
		if _, ierr := testHandler.Queries.InsertReleaseIntegration(ctx, db.InsertReleaseIntegrationParams{
			WorkspaceID:     testUUID(wsID),
			Kind:            "webhook",
			Config:          []byte(`{}`),
			SecretEncrypted: seal(url, signing),
			Events:          events,
			Enabled:         enabled,
			ProbeStatus:     "ok",
			CreatedBy:       testUUID(testUserID),
		}); ierr != nil {
			t.Fatalf("insert integration: %v", ierr)
		}
	}
	insert(matchSrv.URL, "sig-match", []string{"deploy_recorded"}, true)
	insert(disabledSrv.URL, "", []string{"deploy_recorded"}, false)
	insert(otherSrv.URL, "", []string{"release_shipped"}, true)

	// Fire deploy:recorded synchronously (fanOutReleaseEvent waits for deliveries).
	testHandler.fanOutReleaseEvent(protocol.EventDeployRecorded, events.Event{
		WorkspaceID: wsID,
		Payload: map[string]any{
			"issue_id": "issue-1",
			"ref":      "main",
			"target":   "qa-box",
			"status":   "success",
		},
	})

	if got := matchRec.hits.Load(); got != 1 {
		t.Errorf("matching enabled integration: hits = %d, want 1", got)
	}
	if got := disabledRec.hits.Load(); got != 0 {
		t.Errorf("disabled integration: hits = %d, want 0", got)
	}
	if got := otherEventRec.hits.Load(); got != 0 {
		t.Errorf("non-matching-events integration: hits = %d, want 0", got)
	}

	// The delivered body is signed with the integration's signing secret.
	if body, ok := matchRec.body.Load().([]byte); ok {
		if sig, _ := matchRec.sig.Load().(string); sig != releasehook.Sign("sig-match", body) {
			t.Errorf("delivered signature does not verify over the body")
		}
	} else {
		t.Error("no body recorded on the matching receiver")
	}
}
