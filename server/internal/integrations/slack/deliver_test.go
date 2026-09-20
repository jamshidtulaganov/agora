package slack

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock is the injected clock seam. Time only moves when the code under
// test sleeps, which makes every assertion about pacing exact instead of
// timing-dependent — a real 1-second bucket in a unit test is a flake waiting
// for a loaded CI box.
type testClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
	// holdFor + hold let a test freeze the coalescing window open so it can
	// enqueue a burst before the flush runs.
	holdFor time.Duration
	hold    chan struct{}
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Sleep(ctx context.Context, d time.Duration) bool {
	c.mu.Lock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	var gate chan struct{}
	if c.hold != nil && d == c.holdFor {
		gate = c.hold
	}
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return false
		}
	}
	return ctx.Err() == nil
}

func (c *testClock) sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]time.Duration, len(c.slept))
	copy(out, c.slept)
	return out
}

func (c *testClock) slept_(d time.Duration) bool {
	for _, s := range c.sleeps() {
		if s == d {
			return true
		}
	}
	return false
}

// recordingPoster stands in for chat.postMessage.
type recordingPoster struct {
	clock *testClock
	mu    sync.Mutex
	calls []PostMessageRequest
	at    []time.Time
	// errs is consumed one per call; a short list means "nil from then on".
	errs []error
}

func (p *recordingPoster) PostMessage(_ context.Context, _ string, req PostMessageRequest) (PostMessageResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, req)
	if p.clock != nil {
		p.at = append(p.at, p.clock.Now())
	}
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		if err != nil {
			return PostMessageResult{}, err
		}
	}
	return PostMessageResult{Channel: req.Channel, TS: "1700000000.000100"}, nil
}

func (p *recordingPoster) snapshot() ([]PostMessageRequest, []time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	calls := make([]PostMessageRequest, len(p.calls))
	copy(calls, p.calls)
	at := make([]time.Time, len(p.at))
	copy(at, p.at)
	return calls, at
}

func testMessage(issueID, identifier string) Message {
	return Message{
		InstallationID: "inst-1",
		Token:          "xoxb-test",
		ChannelID:      "C0ENG",
		Kind:           "failed",
		Emoji:          ":x:",
		Singular:       "Task failed",
		Plural:         "tasks failed",
		Item: Item{
			ID:         issueID,
			Identifier: identifier,
			Title:      "Something broke",
			URL:        "https://agora.test/ws/issues/" + identifier,
		},
	}
}

// A burst of events for one channel and one kind must become ONE message —
// the whole reason the worker exists rather than a goroutine per event.
func TestDispatcherCoalescesBurstIntoOneMessage(t *testing.T) {
	clock := newTestClock()
	clock.holdFor = 10 * time.Second
	clock.hold = make(chan struct{})
	poster := &recordingPoster{clock: clock}

	d := NewDispatcher(context.Background(), poster, Options{
		Window: 10 * time.Second,
		Now:    clock.Now,
		Sleep:  clock.Sleep,
	})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	// Wait until the flusher is parked inside the window before adding more,
	// so the test exercises the merge and not a race it happened to win.
	waitFor(t, func() bool { return clock.slept_(10 * time.Second) })
	d.Enqueue(testMessage("issue-2", "MUL-2"))
	d.Enqueue(testMessage("issue-3", "MUL-3"))
	// The same issue twice inside one window is ONE line: an inbox fanout
	// publishes one event per recipient.
	d.Enqueue(testMessage("issue-2", "MUL-2"))
	close(clock.hold)
	d.Wait()

	calls, _ := poster.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one coalesced message, got %d", len(calls))
	}
	text := blocksText(calls[0].Blocks)
	if !strings.Contains(text, "3 tasks failed") {
		t.Fatalf("expected a coalesced headline, got %q", text)
	}
	for _, ident := range []string{"MUL-1", "MUL-2", "MUL-3"} {
		if !strings.Contains(text, ident) {
			t.Fatalf("coalesced message lost %s: %q", ident, text)
		}
	}
	if strings.Count(text, "|MUL-2>") != 1 {
		t.Fatalf("duplicate issue was not deduped: %q", text)
	}
}

// Different kinds never merge: "3 tasks failed" and "2 issues created" are
// different sentences, so they are different batches — and therefore the
// per-channel bucket is what keeps them a second apart.
func TestDispatcherPacesOneMessagePerSecondPerChannel(t *testing.T) {
	clock := newTestClock()
	poster := &recordingPoster{clock: clock}
	d := NewDispatcher(context.Background(), poster, Options{
		// A nanosecond window still exercises the flush path; zero would mean
		// "unset" and fall back to the production 10s default.
		Window:   time.Nanosecond,
		Interval: time.Second,
		Now:      clock.Now,
		Sleep:    clock.Sleep,
	})

	first := testMessage("issue-1", "MUL-1")
	d.Enqueue(first)
	d.Wait()

	second := testMessage("issue-2", "MUL-2")
	second.Kind = "created"
	second.Singular = "Issue created"
	second.Plural = "issues created"
	d.Enqueue(second)
	d.Wait()

	calls, at := poster.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected two messages, got %d", len(calls))
	}
	if gap := at[1].Sub(at[0]); gap < time.Second {
		t.Fatalf("two messages to one channel were %s apart; chat.postMessage allows 1/sec/channel", gap)
	}
}

// A different channel has its own bucket — one busy channel must not pace a
// quiet one.
func TestDispatcherBucketsArePerChannel(t *testing.T) {
	clock := newTestClock()
	poster := &recordingPoster{clock: clock}
	d := NewDispatcher(context.Background(), poster, Options{
		// A nanosecond window still exercises the flush path; zero would mean
		// "unset" and fall back to the production 10s default.
		Window:   time.Nanosecond,
		Interval: time.Second,
		Now:      clock.Now,
		Sleep:    clock.Sleep,
	})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	d.Wait()
	other := testMessage("issue-2", "MUL-2")
	other.ChannelID = "C0DESIGN"
	d.Enqueue(other)
	d.Wait()

	_, at := poster.snapshot()
	if len(at) != 2 {
		t.Fatalf("expected two messages, got %d", len(at))
	}
	if gap := at[1].Sub(at[0]); gap >= time.Second {
		t.Fatalf("a second channel waited %s behind the first; buckets are per channel", gap)
	}
}

// Slack names the exact delay on a 429. We sleep that, not a guess: a shorter
// guess is a retry storm and a longer one delays a notification nobody asked
// us to delay.
func TestDispatcherHonoursRetryAfterOn429(t *testing.T) {
	clock := newTestClock()
	poster := &recordingPoster{
		clock: clock,
		errs: []error{&APIError{
			Method:     "chat.postMessage",
			Code:       rateLimitedCode,
			StatusCode: http.StatusTooManyRequests,
			RetryAfter: 3 * time.Second,
		}},
	}
	d := NewDispatcher(context.Background(), poster, Options{
		Window:   time.Nanosecond,
		Interval: 0,
		Now:      clock.Now,
		Sleep:    clock.Sleep,
	})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	d.Wait()

	calls, _ := poster.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected one retry after the 429, got %d attempts", len(calls))
	}
	if !clock.slept_(3 * time.Second) {
		t.Fatalf("expected a 3s sleep from Retry-After, slept %v", clock.sleeps())
	}
}

// Three attempts then drop. A dropped notification is a cost; a retry storm is
// an outage.
func TestDispatcherGivesUpAfterMaxAttempts(t *testing.T) {
	clock := newTestClock()
	rateLimited := &APIError{Method: "chat.postMessage", Code: rateLimitedCode, StatusCode: http.StatusTooManyRequests, RetryAfter: time.Second}
	poster := &recordingPoster{clock: clock, errs: []error{rateLimited, rateLimited, rateLimited, rateLimited}}
	d := NewDispatcher(context.Background(), poster, Options{
		Window: time.Nanosecond,
		Now:    clock.Now,
		Sleep:  clock.Sleep,
	})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	d.Wait()

	calls, _ := poster.snapshot()
	if len(calls) != DefaultMaxAttempts {
		t.Fatalf("expected %d attempts, got %d", DefaultMaxAttempts, len(calls))
	}
}

// An application error is not retryable: attempt two fails identically.
func TestDispatcherDropsApplicationErrorWithoutRetry(t *testing.T) {
	clock := newTestClock()
	poster := &recordingPoster{
		clock: clock,
		errs:  []error{&APIError{Method: "chat.postMessage", Code: "channel_not_found", StatusCode: 200}},
	}
	d := NewDispatcher(context.Background(), poster, Options{Window: time.Nanosecond, Now: clock.Now, Sleep: clock.Sleep})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	d.Wait()

	calls, _ := poster.snapshot()
	if len(calls) != 1 {
		t.Fatalf("a channel_not_found must not be retried; got %d attempts", len(calls))
	}
}

// A dead token stops the installation instead of failing forever.
func TestDispatcherReportsInvalidToken(t *testing.T) {
	clock := newTestClock()
	poster := &recordingPoster{
		clock: clock,
		errs:  []error{&APIError{Method: "chat.postMessage", Code: "token_revoked", StatusCode: 200}},
	}
	var revoked []string
	var mu sync.Mutex
	d := NewDispatcher(context.Background(), poster, Options{
		Window: time.Nanosecond, Now: clock.Now, Sleep: clock.Sleep,
		OnTokenInvalid: func(id string) {
			mu.Lock()
			defer mu.Unlock()
			revoked = append(revoked, id)
		},
	})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	d.Wait()

	calls, _ := poster.snapshot()
	if len(calls) != 1 {
		t.Fatalf("a revoked token must not be retried; got %d attempts", len(calls))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(revoked) != 1 || revoked[0] != "inst-1" {
		t.Fatalf("expected the installation to be reported revoked, got %v", revoked)
	}
}

// A cancelled context must abandon in-flight waits rather than post after the
// process has been told to stop.
func TestDispatcherStopsWithContext(t *testing.T) {
	clock := newTestClock()
	clock.holdFor = 10 * time.Second
	clock.hold = make(chan struct{})
	poster := &recordingPoster{clock: clock}
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDispatcher(ctx, poster, Options{Window: 10 * time.Second, Now: clock.Now, Sleep: clock.Sleep})

	d.Enqueue(testMessage("issue-1", "MUL-1"))
	waitFor(t, func() bool { return clock.slept_(10 * time.Second) })
	cancel()
	d.Wait()

	calls, _ := poster.snapshot()
	if len(calls) != 0 {
		t.Fatalf("expected nothing posted after cancellation, got %d", len(calls))
	}
	// And a message enqueued after the context ended is dropped, not queued
	// against a dispatcher that will never drain.
	d.Enqueue(testMessage("issue-2", "MUL-2"))
	if p := d.Pending(); p != 0 {
		t.Fatalf("expected no pending batches after cancellation, got %d", p)
	}
}

func TestRenderNotificationSingleEventCarriesLinkAndActor(t *testing.T) {
	head := testMessage("issue-1", "MUL-12")
	head.Actor = "by Alice"
	head.Detail = "todo → in progress"
	text, blocks := RenderNotification(head, []Item{head.Item}, 0)

	if !strings.Contains(text, "Task failed") || !strings.Contains(text, "MUL-12") {
		t.Fatalf("fallback text = %q", text)
	}
	body := blocksText(blocks)
	if !strings.Contains(body, "<https://agora.test/ws/issues/MUL-12|MUL-12>") {
		t.Fatalf("expected a linked identifier, got %q", body)
	}
	if !strings.Contains(body, "by Alice") || !strings.Contains(body, "todo → in progress") {
		t.Fatalf("expected the actor and detail context line, got %q", body)
	}
}

func TestRenderNotificationCapsListedItems(t *testing.T) {
	head := testMessage("issue-0", "MUL-0")
	items := make([]Item, 0, maxBatchItems)
	for i := 0; i < maxBatchItems; i++ {
		items = append(items, Item{ID: "i", Identifier: "MUL-" + string(rune('A'+i)), URL: "https://agora.test/x"})
	}
	_, blocks := RenderNotification(head, items, 7)
	body := blocksText(blocks)
	if !strings.Contains(body, "and 7 more") {
		t.Fatalf("expected the overflow line, got %q", body)
	}
	if !strings.Contains(body, "17 tasks failed") {
		t.Fatalf("headline must count the dropped items too, got %q", body)
	}
}

// waitFor spins until cond holds or the test times out. Used only to
// synchronise with a goroutine that has parked on the injected clock.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for the dispatcher to reach the coalescing window")
}

// blocksText flattens a rendered payload so an assertion can look for copy
// without knowing which block carries it.
func blocksText(blocks []Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		collectText(b, &sb)
	}
	return sb.String()
}

func collectText(v any, sb *strings.Builder) {
	switch value := v.(type) {
	case Block:
		for _, inner := range value {
			collectText(inner, sb)
		}
	case map[string]any:
		for key, inner := range value {
			if key == "type" {
				continue
			}
			collectText(inner, sb)
		}
	case []any:
		for _, inner := range value {
			collectText(inner, sb)
		}
	case string:
		sb.WriteString(value + "\n")
	}
}
