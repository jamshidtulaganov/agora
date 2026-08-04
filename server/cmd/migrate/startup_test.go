package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type scriptedPinger struct {
	errors []error
	calls  int
}

func (p *scriptedPinger) Ping(context.Context) error {
	p.calls++
	if len(p.errors) == 0 {
		return nil
	}
	err := p.errors[0]
	p.errors = p.errors[1:]
	return err
}

func TestWaitForDatabaseRetriesUntilReady(t *testing.T) {
	db := &scriptedPinger{errors: []error{
		errors.New("connection refused"),
		errors.New("connection refused"),
	}}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForDatabase(ctx, db, time.Millisecond); err != nil {
		t.Fatalf("waitForDatabase() error = %v", err)
	}
	if db.calls != 3 {
		t.Fatalf("Ping() calls = %d, want 3", db.calls)
	}
}

func TestWaitForDatabaseStopsAtDeadline(t *testing.T) {
	db := &scriptedPinger{errors: []error{errors.New("connection refused")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := waitForDatabase(ctx, db, time.Hour)
	if err == nil {
		t.Fatal("waitForDatabase() error = nil, want deadline error")
	}
	if !strings.Contains(err.Error(), "database did not become ready after 1 attempts") {
		t.Fatalf("waitForDatabase() error = %q", err)
	}
	if db.calls != 1 {
		t.Fatalf("Ping() calls = %d, want 1", db.calls)
	}
}
