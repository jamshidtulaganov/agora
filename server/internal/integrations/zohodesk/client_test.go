package zohodesk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type staticTokens struct{ refreshed int }

func (s *staticTokens) AccessToken(context.Context) (string, error) { return "at-1", nil }
func (s *staticTokens) ForceRefresh(context.Context, string) (string, error) {
	s.refreshed++
	return "at-2", nil
}

func TestIDDecodesStringsNumbersAndNull(t *testing.T) {
	var v struct {
		A ID   `json:"a"`
		B ID   `json:"b"`
		C ID   `json:"c"`
		D []ID `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"a":"501","b":600100,"c":null,"d":["1",2]}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != "501" || v.B != "600100" || v.C != "" || len(v.D) != 2 || v.D[1] != "2" {
		t.Fatalf("decoded %+v", v)
	}
}

func TestClientSendsOrgRetriesOn401AndReads204AsEmpty(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("orgId") != "42" {
			t.Errorf("missing orgId header on %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "Zoho-oauthtoken at-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tokens := &staticTokens{}
	c, err := New(tokens, "us", srv.URL, "42")
	if err != nil {
		t.Fatal(err)
	}
	list, err := c.ListTickets(context.Background(), TicketQuery{})
	if err != nil || len(list) != 0 || tokens.refreshed != 1 || calls != 2 {
		t.Fatalf("list=%v err=%v refreshed=%d calls=%d", list, err, tokens.refreshed, calls)
	}
	if _, err := c.WithOrg("").MyInfo(context.Background()); err == nil {
		t.Fatal("expected an error without an org")
	}
}
