package zohocrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// stubZoho fakes the accounts token endpoint + CRM API on one server.
type stubZoho struct {
	srv        *httptest.Server
	tokenCalls atomic.Int64
	authHeader atomic.Value // last Authorization header seen by the API
	rejectAuth bool         // token endpoint returns {"error": "invalid_code"}

	mu         sync.Mutex
	coqlPages  []coqlPage     // consumed one per COQL call; empty -> 204
	lastCOQL   string         // last select_query received
	updateFail bool           // PUT returns a per-record error status
	updatePath string         // path of the last record PUT
	lastUpdate map[string]any // decoded body of the last record PUT
}

// coqlPage is one COQL response page.
type coqlPage struct {
	data []map[string]any
	more bool
}

func newStubZoho(t *testing.T) *stubZoho {
	t.Helper()
	s := &stubZoho{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v2/token", func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if s.rejectAuth {
			// Zoho reports grant errors as HTTP 200 + {"error": ...}.
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_code"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "expires_in": 3600})
	})
	mux.HandleFunc("/crm/v8/org", func(w http.ResponseWriter, r *http.Request) {
		s.authHeader.Store(r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(map[string]any{"org": []map[string]any{{"id": "42", "company_name": "Octane"}}})
	})
	mux.HandleFunc("/crm/v8/settings/modules", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"modules": []map[string]any{
			{"api_name": "Tasks", "generated_type": "default", "api_supported": true, "creatable": true},
			{"api_name": "CustomModule34", "module_name": "Tickets", "generated_type": "custom", "api_supported": true},
			{"api_name": "Subform_2", "generated_type": "subform", "api_supported": true},
			{"api_name": "Hidden", "generated_type": "default", "api_supported": false},
		}})
	})
	mux.HandleFunc("/crm/v8/settings/fields", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("module") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"fields": []map[string]any{
			{"api_name": "Subject", "field_label": "Subject", "data_type": "text"},
			{"api_name": "Status", "field_label": "Status", "data_type": "picklist",
				"pick_list_values": []map[string]string{{"display_value": "Open", "actual_value": "Open"}}},
		}})
	})
	// Method-qualified patterns so the record surface cannot conflict with the
	// method-less discovery routes above.
	mux.HandleFunc("POST /crm/v8/coql", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var body struct {
			SelectQuery string `json:"select_query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.lastCOQL = body.SelectQuery
		if len(s.coqlPages) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		page := s.coqlPages[0]
		s.coqlPages = s.coqlPages[1:]
		json.NewEncoder(w).Encode(map[string]any{
			"data": page.data,
			"info": map[string]any{"more_records": page.more},
		})
	})
	mux.HandleFunc("PUT /crm/v8/Tasks", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.updatePath = r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.lastUpdate = body
		if s.updateFail {
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"status": "error", "code": "INVALID_DATA", "message": "blueprint transition rejected"},
			}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"status": "success", "code": "SUCCESS"},
		}})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func newTestClient(t *testing.T, s *stubZoho) *Client {
	t.Helper()
	c, err := New("cid", "csecret", "rtoken", "us", s.srv.URL, s.srv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestAccessTokenCached(t *testing.T) {
	s := newStubZoho(t)
	c := newTestClient(t, s)
	ctx := context.Background()

	if _, err := c.GetOrganization(ctx); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := c.ListModules(ctx); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if got := s.tokenCalls.Load(); got != 1 {
		t.Fatalf("token minted %d times across two API calls, want 1 (cache)", got)
	}
	if h, _ := s.authHeader.Load().(string); h != "Zoho-oauthtoken at-1" {
		t.Fatalf("Authorization header = %q", h)
	}
}

func TestAuthErrorClassification(t *testing.T) {
	s := newStubZoho(t)
	s.rejectAuth = true
	c := newTestClient(t, s)

	_, err := c.GetOrganization(context.Background())
	if !IsAuthError(err) {
		t.Fatalf("expected AuthError for rejected grant, got: %v", err)
	}
}

func TestListModulesFiltersNonSyncable(t *testing.T) {
	s := newStubZoho(t)
	c := newTestClient(t, s)

	mods, err := c.ListModules(context.Background())
	if err != nil {
		t.Fatalf("ListModules: %v", err)
	}
	if len(mods) != 2 {
		t.Fatalf("got %d modules, want 2 (default+custom, api_supported only): %+v", len(mods), mods)
	}
	names := map[string]bool{}
	for _, m := range mods {
		names[m.APIName] = true
	}
	if !names["Tasks"] || !names["CustomModule34"] {
		t.Fatalf("filtered set wrong: %+v", names)
	}
}

func TestListFields(t *testing.T) {
	s := newStubZoho(t)
	c := newTestClient(t, s)

	fields, err := c.ListFields(context.Background(), "Tasks")
	if err != nil {
		t.Fatalf("ListFields: %v", err)
	}
	if len(fields) != 2 || fields[1].DataType != "picklist" || len(fields[1].PickListValues) != 1 {
		t.Fatalf("fields projection wrong: %+v", fields)
	}
}

func TestUnknownDCRejected(t *testing.T) {
	if _, err := New("c", "s", "r", "mars", "", ""); err == nil {
		t.Fatal("expected error for unknown dc")
	}
}

func TestZohoCRMQueryPagesAndEmpty(t *testing.T) {
	s := newStubZoho(t)
	s.coqlPages = []coqlPage{
		{data: []map[string]any{{"id": "1", "Subject": "A"}, {"id": "2"}}, more: true},
		{data: []map[string]any{{"id": "3"}}, more: false},
	}
	c := newTestClient(t, s)
	ctx := context.Background()

	coql := "SELECT id, Subject FROM Tasks WHERE Modified_Time > '1970-01-01T00:00:00+00:00' ORDER BY Modified_Time ASC LIMIT 200"
	recs, more, err := c.Query(ctx, coql)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(recs) != 2 || !more {
		t.Fatalf("first page: got %d records, more=%v; want 2, true", len(recs), more)
	}
	if got, _ := recs[0]["Subject"].(string); got != "A" {
		t.Fatalf("record payload lost: %+v", recs[0])
	}
	s.mu.Lock()
	gotCOQL := s.lastCOQL
	s.mu.Unlock()
	if gotCOQL != coql {
		t.Fatalf("select_query = %q, want %q", gotCOQL, coql)
	}

	recs, more, err = c.Query(ctx, coql)
	if err != nil || len(recs) != 1 || more {
		t.Fatalf("second page: recs=%d more=%v err=%v; want 1, false, nil", len(recs), more, err)
	}

	// Exhausted pages -> Zoho answers 204 No Content: empty result, no error.
	recs, more, err = c.Query(ctx, coql)
	if err != nil || len(recs) != 0 || more {
		t.Fatalf("empty page: recs=%d more=%v err=%v; want 0, false, nil", len(recs), more, err)
	}
}

func TestZohoCRMUpdateRecord(t *testing.T) {
	s := newStubZoho(t)
	c := newTestClient(t, s)
	ctx := context.Background()

	if err := c.UpdateRecord(ctx, "Tasks", "42", map[string]any{"Status": "Closed"}); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	s.mu.Lock()
	path, body := s.updatePath, s.lastUpdate
	s.mu.Unlock()
	if path != "/crm/v8/Tasks" {
		t.Fatalf("update path = %q", path)
	}
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("update body shape: %+v", body)
	}
	rec, _ := data[0].(map[string]any)
	if rec["id"] != "42" || rec["Status"] != "Closed" {
		t.Fatalf("update record = %+v, want id=42 Status=Closed", rec)
	}

	// Zoho reports per-record failures inside a 2xx envelope; the client must
	// surface them as errors.
	s.mu.Lock()
	s.updateFail = true
	s.mu.Unlock()
	err := c.UpdateRecord(ctx, "Tasks", "42", map[string]any{"Status": "Closed"})
	if err == nil || !strings.Contains(err.Error(), "INVALID_DATA") {
		t.Fatalf("expected per-record error, got: %v", err)
	}
}
