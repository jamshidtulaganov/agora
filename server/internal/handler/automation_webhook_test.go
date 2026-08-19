package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/releasehook"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

func TestAutomationSendWebhookUsesEncryptedWorkspaceConnector(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	t.Setenv("AGORA_RELEASE_SECRET_KEY", releaseTestKey(t))
	resetReleaseBox()
	t.Cleanup(resetReleaseBox)

	type delivery struct {
		body      []byte
		signature string
		event     string
	}
	received := make(chan delivery, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- delivery{
			body: body, signature: r.Header.Get(releasehook.SignatureHeader), event: r.Header.Get(releasehook.EventHeader),
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	box, err := releaseIntegrationBox()
	if err != nil {
		t.Fatalf("release integration box: %v", err)
	}
	sealed, err := sealWebhookSecret(box, webhookSecret{URL: receiver.URL, Signing: "automation-signing-key"})
	if err != nil {
		t.Fatalf("seal connector: %v", err)
	}
	connector, err := testHandler.Queries.InsertReleaseIntegration(ctx, db.InsertReleaseIntegrationParams{
		WorkspaceID:     testUUID(testWorkspaceID),
		Kind:            "webhook",
		Config:          []byte(`{"name":"n8n test"}`),
		SecretEncrypted: sealed,
		Events:          []string{releaseEventDeployRecorded},
		Enabled:         true,
		ProbeStatus:     "ok",
		CreatedBy:       testUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM release_integration WHERE id = $1`, connector.ID)
	})

	issueID := sliceActionTestIssue(t, "", "")
	issue, err := testHandler.Queries.GetIssue(ctx, testUUID(issueID))
	if err != nil {
		t.Fatalf("load issue: %v", err)
	}
	rule := db.Automation{ID: issue.ID, WorkspaceID: issue.WorkspaceID, Name: "Notify n8n"}
	detail, err := testHandler.automationSendWebhook(ctx, rule, AutomationEvent{
		Trigger: automationTriggerStatusChanged, Issue: issue,
	}, issue, automationAction{
		Type: automationActionSendWebhook,
		Config: map[string]string{
			"integration_id": uuidToString(connector.ID),
			"message":        "{{issue}} — {{title}} is {{status}}",
		},
	})
	if err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	if detail == "" {
		t.Fatal("successful delivery must return an audit detail")
	}

	got := <-received
	if got.event != automationWebhookEvent {
		t.Errorf("event header = %q, want %q", got.event, automationWebhookEvent)
	}
	if got.signature != releasehook.Sign("automation-signing-key", got.body) {
		t.Error("delivery signature does not verify")
	}
	var payload map[string]any
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["event"] != automationWebhookEvent || payload["delivery_id"] == "" {
		t.Errorf("payload missing event/delivery id: %v", payload)
	}
	issuePayload, _ := payload["issue"].(map[string]any)
	if issuePayload["id"] != issueID || issuePayload["title"] != issue.Title {
		t.Errorf("payload issue = %v", issuePayload)
	}
}
