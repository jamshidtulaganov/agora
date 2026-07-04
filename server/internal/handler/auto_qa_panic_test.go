package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The auto-QA-on-in_review feature launches DETACHED goroutines (issue.go) that
// author comments as the transition actor. They used to call parseUUID(actorID)
// (util.MustParseUUID — panics on invalid input). actorID comes from
// resolveActor, whose X-Actor-Source: task_token branch returned the raw
// X-Agent-ID header verbatim. An empty/malformed X-Agent-ID therefore panicked
// INSIDE a detached goroutine — which chi's middleware.Recoverer does NOT cover
// — crashing the whole server process for every tenant. These tests pin the
// three-layer fix.

// SECURITY (MUL-2600): a task_token actor is ALWAYS actor=agent, even when
// X-Agent-ID is stripped/empty — resolveActor must NOT fall back to the member
// path, or an agent could drop its identity headers to escape agent-only guards
// (TestAgentEnv_TaskTokenActorSource). The malformed-id crash is closed
// downstream (actorAuthorID + safeGo), NOT by weakening this invariant.
func TestResolveActor_TaskTokenAlwaysAgent(t *testing.T) {
	h := &Handler{}
	for _, agentID := range []string{"", "not-a-uuid", "   ", "11111111-1111-1111-1111-111111111111"} {
		req := httptest.NewRequest(http.MethodPatch, "/api/issues/x", nil)
		req.Header.Set("X-Actor-Source", "task_token")
		if agentID != "" {
			req.Header.Set("X-Agent-ID", agentID)
		}
		typ, id := h.resolveActor(req, "member-uuid", "ws-uuid")
		if typ != "agent" || id != agentID {
			t.Fatalf("X-Agent-ID=%q: got (%q,%q), want (agent, %q)", agentID, typ, id, agentID)
		}
		// The downstream author resolver is what keeps a malformed agent id from
		// panicking: a non-UUID id is rejected (authoring skipped), a real one
		// is accepted.
		_, ok := actorAuthorID(id)
		wantOK := id == "11111111-1111-1111-1111-111111111111"
		if ok != wantOK {
			t.Fatalf("actorAuthorID(%q) ok=%v, want %v", id, ok, wantOK)
		}
	}
}

// Layer 2: the comment-author resolver rejects a malformed id (ok=false) so the
// caller skips authoring instead of feeding a bad string to MustParseUUID.
func TestActorAuthorID_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "nope", "   ", "1234"} {
		if _, ok := actorAuthorID(bad); ok {
			t.Errorf("actorAuthorID(%q) = ok, want rejected", bad)
		}
	}
	valid := "22222222-2222-2222-2222-222222222222"
	if u, ok := actorAuthorID(valid); !ok || !u.Valid {
		t.Errorf("actorAuthorID(%q): ok=%v valid=%v, want accepted", valid, ok, u.Valid)
	}
}

// Layer 1: safeGo contains a panic inside a detached goroutine — the exact crash
// chain (a bad-UUID parse) no longer takes down the process. If recover were
// absent, the first safeGo's panic crashes the process and `go test` aborts, so
// reaching (and passing) the second safeGo assertion IS the proof of recovery.
func TestSafeGo_ContainsDetachedGoroutinePanic(t *testing.T) {
	entered := make(chan struct{})
	safeGo("test-panic", func() {
		close(entered)              // signal entry, right before the panic
		_ = parseUUID("not-a-uuid") // util.MustParseUUID panics — must be recovered
		t.Error("unreachable: line after the panicking parse ran")
	})
	<-entered
	time.Sleep(50 * time.Millisecond) // let it unwind through safeGo's recover

	// The process is still healthy: a fresh safeGo runs to completion.
	ran := make(chan struct{})
	safeGo("test-normal", func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("process did not survive a panicking detached goroutine")
	}
}
