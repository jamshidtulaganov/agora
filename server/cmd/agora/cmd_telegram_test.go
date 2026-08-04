package main

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/cli"
)

func TestAskTimeoutMirrorsServerBounds(t *testing.T) {
	if got := askTimeout(0); got != 10*time.Minute {
		t.Fatalf("default = %v, want 10m", got)
	}
	if got := askTimeout(120); got != 2*time.Minute {
		t.Fatalf("explicit = %v, want 2m", got)
	}
	if got := askTimeout(99999); got != 60*time.Minute {
		t.Fatalf("cap = %v, want 60m", got)
	}
}

func TestTelegramAskPollErrorClassification(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
	} {
		if !telegramAskPollErrorIsPermanent(&cli.HTTPError{StatusCode: status}) {
			t.Errorf("status %d should stop polling", status)
		}
	}
	for _, err := range []error{
		errors.New("temporary network failure"),
		&cli.HTTPError{StatusCode: http.StatusTooManyRequests},
		&cli.HTTPError{StatusCode: http.StatusBadGateway},
	} {
		if telegramAskPollErrorIsPermanent(err) {
			t.Errorf("%v should remain retryable", err)
		}
	}
}
