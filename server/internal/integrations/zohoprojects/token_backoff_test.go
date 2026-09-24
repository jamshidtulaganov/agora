package zohoprojects

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A failed grant is not retried on every call: Zoho's "Access Denied" throttle
// is kept alive by exactly that burst.
func TestClientTokenFailureBacksOff(t *testing.T) {
	grants := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/oauth/v2/token") {
			grants++
			io.WriteString(w, `{"error":"Access Denied"}`)
			return
		}
		io.WriteString(w, `{"projects":[]}`)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(Config{ClientID: "c", ClientSecret: "s", RefreshToken: "r", AccountsHost: srv.URL, APIHost: srv.URL})
	for i := 0; i < 5; i++ {
		if _, err := c.ListProjects(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "Access Denied") {
			t.Fatalf("call %d: err = %v, want Access Denied", i, err)
		}
	}
	if grants != 1 {
		t.Errorf("token grants = %d, want 1 within the backoff window", grants)
	}
}
