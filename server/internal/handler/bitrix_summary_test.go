package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The whole point of the Bitrix-summary endpoint is the HUMAN approval gate: an
// agent must never self-post its final summary to Bitrix. The route is wrapped in
// RequireHumanActor; this pins that a machine credential (an agent's task token)
// is rejected with 403 BEFORE the handler runs — no Bitrix write, no DB access.
func TestPostBitrixSummary_RejectsAgentActor(t *testing.T) {
	h := &Handler{}
	for _, actorSource := range []string{"task_token", "cloud_pat"} {
		t.Run(actorSource, func(t *testing.T) {
			mw := RequireHumanActor(http.HandlerFunc(h.PostBitrixSummary))
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/issues/MUL-1/bitrix-summary",
				strings.NewReader(`{"text":"branch: fix/x — cause: null deref"}`),
			)
			// What middleware/auth stamps for an agent's task token / cloud PAT.
			req.Header.Set("X-Actor-Source", actorSource)
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)

			if w.Code != http.StatusForbidden {
				t.Fatalf("actor %q: want 403 (human-only gate), got %d", actorSource, w.Code)
			}
		})
	}
}
