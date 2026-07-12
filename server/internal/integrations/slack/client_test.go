package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPostMessage: the body is {"text": ...} and a 2xx is a success.
func TestPostMessage(t *testing.T) {
	got := make(chan []byte, 1)
	ct := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
		ct <- r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := NewClient().PostMessage(context.Background(), srv.URL, "🚀 shipped to production"); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if c := <-ct; c != "application/json" {
		t.Errorf("content-type = %q, want application/json", c)
	}
	var m map[string]any
	if err := json.Unmarshal(<-got, &m); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if m["text"] != "🚀 shipped to production" {
		t.Errorf("body text = %v, want the message", m["text"])
	}
}

// TestPostMessageNon2xxIsError: a 4xx (bad payload / expired hook) surfaces an
// error without panicking.
func TestPostMessageNon2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	if err := NewClient().PostMessage(context.Background(), srv.URL, "x"); err == nil {
		t.Fatal("expected an error on a 400 response")
	}
}
