package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssistantArtifactLibraryOwnershipAndPaging(t *testing.T) {
	if testHandler == nil {
		t.Skip("no database")
	}
	owner := newAssistantTestUser(t, "artifact-library-owner@agora.dev")
	other := newAssistantTestUser(t, "artifact-library-other@agora.dev")
	for _, user := range []string{owner, other} {
		session := newAssistantTestSession(t, user)
		if _, err := testPool.Exec(context.Background(), `INSERT INTO assistant_artifact(session_id,user_id,title,kind,content) VALUES($1,$2,$3,'markdown','private content')`, session, user, user); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		query         string
		status, count int
	}{{"", 200, 1}, {"?offset=1", 200, 0}, {"?offset=-1", 400, 0}} {
		w := httptest.NewRecorder()
		testHandler.ListMyAssistantArtifacts(w, newAssistantRequest(http.MethodGet, "/api/assistant/artifacts"+tc.query, owner, ""))
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
		if tc.status != 200 {
			continue
		}
		var items []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
			t.Fatal(err)
		}
		if len(items) != tc.count {
			t.Fatalf("count %d want %d", len(items), tc.count)
		}
		for _, item := range items {
			if item["title"] != owner {
				t.Fatal("another user's artifact leaked")
			}
			if _, ok := item["content"]; ok {
				t.Fatal("library unexpectedly returns bodies")
			}
		}
	}
}
