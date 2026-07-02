package zohocrm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// stubZoho fakes the accounts token endpoint + CRM API on one server.
type stubZoho struct {
	srv        *httptest.Server
	tokenCalls atomic.Int64
	authHeader atomic.Value // last Authorization header seen by the API
	rejectAuth bool         // token endpoint returns {"error": "invalid_code"}
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
