package slack

// Outbound delivery worker (docs/slack-integration-plan.md §Phase 1 —
// "Delivery discipline").
//
// chat.postMessage is rate-limited "Special": ONE message per second per
// channel. A single Agora request can emit dozens of bus events (a bulk status
// change, a squad finishing five tasks), so "spawn a goroutine per event" —
// the Telegram/Lark pattern — would hand Slack a burst it answers with 429s
// and hand the channel a wall of near-identical messages. Both failures are
// the same failure: the team mutes the channel and the integration is dead.
//
// So delivery is a small worker with three rules, and every one of them is a
// rule the plan set before any code existed:
//
//   - A per-channel token bucket at 1/s. Slots are RESERVED under the lock
//     (like rate.Limiter.Reserve), so two concurrent flushes for one channel
//     get consecutive slots instead of racing for the same one.
//   - A coalescing window. Events of the same kind for the same channel that
//     land within the window collapse into ONE message listing each issue —
//     which is both fewer API calls and better copy than three near-identical
//     lines. Deduplicated by issue id, which matters because an inbox fanout
//     publishes one event per recipient for the same issue.
//   - Retry only what a retry can fix. A 429 is slept for exactly the
//     Retry-After Slack named (never a guess, never a storm); a transport
//     failure gets a short backoff; an application error like
//     `channel_not_found` is dropped immediately because attempt two will fail
//     identically. A dead token stops the installation rather than retrying
//     forever.
//
// Nothing here ever blocks or panics into the caller: Enqueue takes a lock,
// appends and returns, and every flush goroutine recovers its own panics. The
// bus is synchronous and on the HTTP request path — a delivery worker that can
// make a write slow is a worse bug than a dropped notification.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Defaults. Each is overridable through Options so a test can run the whole
// machine on a fake clock.
const (
	// DefaultCoalesceWindow is how long a batch collects before it is sent.
	// The plan's number. It buys the burst collapse; the cost is that a lone
	// notification is up to this late, which for "something needs a human" is
	// the right trade against a channel nobody reads.
	DefaultCoalesceWindow = 10 * time.Second
	// DefaultChannelInterval is Slack's documented chat.postMessage ceiling:
	// one message per second per channel.
	DefaultChannelInterval = time.Second
	// DefaultMaxAttempts bounds retries per message.
	DefaultMaxAttempts = 3
	// minRetryDelay is used when a 429 arrives without a parseable
	// Retry-After. Slack always sends one; this is the floor if it ever does
	// not, so we never hot-loop.
	minRetryDelay = time.Second
	// maxBatchItems caps how many issues one coalesced message lists. Beyond
	// it the message says "and N more" — a 50-line Slack message is not a
	// notification, and Block Kit caps the payload anyway.
	maxBatchItems = 10
	// maxPendingBatches bounds the queue. A runaway publisher drops messages
	// with a log line rather than growing the heap of a live server.
	maxPendingBatches = 512
)

// Poster is the one Slack call delivery makes. *APIClient satisfies it; tests
// substitute a recorder.
type Poster interface {
	PostMessage(ctx context.Context, token string, req PostMessageRequest) (PostMessageResult, error)
}

// Item is one issue named by a notification.
type Item struct {
	// ID is the dedupe key — the Agora issue id. Two events for the same
	// issue inside one window produce one line.
	ID string
	// Identifier is the human key (MUL-123) used as the link label.
	Identifier string
	Title      string
	// URL is the canonical Agora issue link. Plain and canonical on purpose:
	// it is the shape PR 3's link parser recognises, so the same string a
	// teammate copies out of the channel unfurls when they paste it back.
	URL string
}

// Message is one notification handed to the worker.
type Message struct {
	// InstallationID identifies the installation whose token this is, so a
	// dead-token failure can mark the right row revoked.
	InstallationID string
	// Token is the UNSEALED bot token. It lives in the queue only as long as
	// the message does and is never logged.
	Token     string
	ChannelID string
	// Kind is the route event kind ("failed", "qa_verdict", …). It is the
	// coalescing axis: different kinds never merge, because "3 tasks failed"
	// and "2 issues created" are different sentences.
	Kind string
	// Emoji leads the headline (":x:", ":white_check_mark:").
	Emoji string
	// Singular / Plural are the headline for one and for many
	// ("Task failed" / "tasks failed" — the plural is rendered as "3 tasks
	// failed", so it carries no leading count).
	Singular string
	Plural   string
	// Actor and Detail are single-event context ("by Alice", "todo → in
	// progress"). A coalesced message drops them: they are per-event facts
	// and a summary that averaged them would be a lie.
	Actor  string
	Detail string
	Item   Item
}

// Options configures a Dispatcher. The zero value is the production config.
type Options struct {
	Window      time.Duration
	Interval    time.Duration
	MaxAttempts int
	// Now and Sleep are the clock seam. Sleep must return false when the
	// context ended (the caller then abandons the delivery).
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) bool
	// OnTokenInvalid is called once per message whose failure proves the
	// installation's token is dead (invalid_auth / token_revoked /
	// account_inactive). The handler marks the row revoked — the same hygiene
	// as an app_uninstalled event.
	OnTokenInvalid func(installationID string)
}

func (o Options) withDefaults() Options {
	if o.Window <= 0 {
		o.Window = DefaultCoalesceWindow
	}
	if o.Interval <= 0 {
		o.Interval = DefaultChannelInterval
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return o
}

// sleepCtx is the real clock's sleep: false means the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// batch is one coalescing bucket: everything queued for one
// (installation, channel, kind) inside one window.
type batch struct {
	head    Message
	items   []Item
	seen    map[string]bool
	dropped int
}

// Dispatcher paces, coalesces and retries outbound Slack messages.
type Dispatcher struct {
	poster Poster
	opts   Options
	ctx    context.Context

	mu sync.Mutex
	// batches is keyed by installation|channel|kind.
	batches map[string]*batch
	// nextSlot is the per-channel token bucket: the earliest time the next
	// message for that channel may be sent.
	nextSlot map[string]time.Time
	stopped  bool

	wg sync.WaitGroup
}

// NewDispatcher starts a worker bound to ctx. Cancelling ctx stops every
// in-flight wait; Wait then returns once the goroutines have unwound.
func NewDispatcher(ctx context.Context, poster Poster, opts Options) *Dispatcher {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Dispatcher{
		poster:   poster,
		opts:     opts.withDefaults(),
		ctx:      ctx,
		batches:  map[string]*batch{},
		nextSlot: map[string]time.Time{},
	}
}

// batchKey is the coalescing axis. Installation is part of it because two
// Agora workspaces on the same Slack team may route the same channel id, and
// their messages must not merge across the tenant boundary.
func batchKey(m Message) string {
	return m.InstallationID + "|" + m.ChannelID + "|" + m.Kind
}

// Enqueue accepts a message for delivery. It never blocks, never returns an
// error and never panics: it is called from a synchronous bus handler on the
// HTTP request path, where every one of those would be a user-visible bug.
func (d *Dispatcher) Enqueue(m Message) {
	if d == nil || d.poster == nil {
		return
	}
	if strings.TrimSpace(m.ChannelID) == "" || strings.TrimSpace(m.Token) == "" {
		return
	}
	key := batchKey(m)

	d.mu.Lock()
	if d.stopped || d.ctx.Err() != nil {
		d.mu.Unlock()
		return
	}
	existing, ok := d.batches[key]
	if !ok {
		if len(d.batches) >= maxPendingBatches {
			d.mu.Unlock()
			slog.Warn("slack delivery: queue full, dropping notification",
				"channel_id", m.ChannelID, "kind", m.Kind)
			return
		}
		b := &batch{head: m, seen: map[string]bool{}}
		b.add(m.Item)
		d.batches[key] = b
		d.wg.Add(1)
		d.mu.Unlock()
		go d.flushAfterWindow(key)
		return
	}
	existing.add(m.Item)
	d.mu.Unlock()
}

// add appends an item unless this issue is already in the batch. The dedupe is
// load-bearing: an inbox fanout publishes one event per recipient, so a
// channel route on `assigned` would otherwise hear the same issue N times.
func (b *batch) add(item Item) {
	key := item.ID
	if strings.TrimSpace(key) == "" {
		key = item.Identifier + "|" + item.URL
	}
	if strings.TrimSpace(key) != "" {
		if b.seen[key] {
			return
		}
		b.seen[key] = true
	}
	if len(b.items) >= maxBatchItems {
		b.dropped++
		return
	}
	b.items = append(b.items, item)
}

// flushAfterWindow waits out the coalescing window, then sends whatever
// collected. One goroutine per open batch; it is the only thing that removes
// the batch from the map, so a later Enqueue for the same key opens a new one.
func (d *Dispatcher) flushAfterWindow(key string) {
	defer d.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("slack delivery: panic recovered", "recovered", r)
		}
	}()

	if !d.opts.Sleep(d.ctx, d.opts.Window) {
		// Shutting down: drop the batch rather than posting into a channel
		// after the process has been told to stop.
		d.take(key)
		return
	}
	b := d.take(key)
	if b == nil || len(b.items) == 0 {
		return
	}
	d.deliver(b)
}

func (d *Dispatcher) take(key string) *batch {
	d.mu.Lock()
	defer d.mu.Unlock()
	b := d.batches[key]
	delete(d.batches, key)
	return b
}

// reserveSlot hands out the next send slot for a channel and advances the
// bucket, returning how long the caller must wait for it. Reserving under the
// lock (rather than "sleep if the last send was recent") is what makes two
// concurrent flushes for one channel serialise instead of colliding.
func (d *Dispatcher) reserveSlot(channelID string) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.opts.Now()
	slot := d.nextSlot[channelID]
	if slot.Before(now) {
		slot = now
	}
	d.nextSlot[channelID] = slot.Add(d.opts.Interval)
	d.pruneSlotsLocked(now)
	return slot.Sub(now)
}

// pruneSlotsLocked drops channels whose bucket has drained. Without it the map
// keeps one entry per channel ever posted to, for the life of the process.
func (d *Dispatcher) pruneSlotsLocked(now time.Time) {
	if len(d.nextSlot) <= maxPendingBatches {
		return
	}
	for ch, slot := range d.nextSlot {
		if slot.Before(now) {
			delete(d.nextSlot, ch)
		}
	}
}

// deliver paces, renders and posts one batch, retrying only what a retry can
// fix. It returns nothing: every outcome is either a sent message or a log
// line, because there is no caller left to tell.
func (d *Dispatcher) deliver(b *batch) {
	if wait := d.reserveSlot(b.head.ChannelID); wait > 0 {
		if !d.opts.Sleep(d.ctx, wait) {
			return
		}
	}
	text, blocks := RenderNotification(b.head, b.items, b.dropped)
	unfurl := false
	req := PostMessageRequest{
		Channel: b.head.ChannelID,
		Text:    text,
		Blocks:  blocks,
		// Our own messages never produce a link_shared event for our own app
		// (Slack does not unfurl an app's own links), so leaving this on would
		// only buy a generic page-title preview under every notification.
		UnfurlLinks: &unfurl,
	}

	for attempt := 1; attempt <= d.opts.MaxAttempts; attempt++ {
		if d.ctx.Err() != nil {
			return
		}
		_, err := d.poster.PostMessage(d.ctx, b.head.Token, req)
		if err == nil {
			return
		}
		switch {
		case IsTokenInvalid(err):
			// Retrying a dead token is how a queue becomes a permanent
			// failure. Stop, and let the installation be marked revoked.
			slog.Warn("slack delivery: installation token rejected",
				"installation_id", b.head.InstallationID, "error", err)
			if d.opts.OnTokenInvalid != nil {
				d.opts.OnTokenInvalid(b.head.InstallationID)
			}
			return
		case IsRateLimited(err):
			delay := retryAfterFrom(err)
			if attempt == d.opts.MaxAttempts {
				break
			}
			if !d.opts.Sleep(d.ctx, delay) {
				return
			}
			continue
		case isTransientFailure(err):
			if attempt == d.opts.MaxAttempts {
				break
			}
			if !d.opts.Sleep(d.ctx, backoffFor(attempt)) {
				return
			}
			continue
		default:
			// An application error (channel_not_found, not_in_channel,
			// invalid_blocks): attempt two fails identically, so the honest
			// answer is one warn line, not three calls.
			slog.Warn("slack delivery: dropped notification",
				"channel_id", b.head.ChannelID, "kind", b.head.Kind, "error", err)
			return
		}
		break
	}
	slog.Warn("slack delivery: giving up after retries",
		"channel_id", b.head.ChannelID, "kind", b.head.Kind, "attempts", d.opts.MaxAttempts)
}

// retryAfterFrom returns exactly the delay Slack named, or the floor when it
// named none. Guessing a longer one delays a notification nobody asked us to
// delay; guessing a shorter one is a retry storm.
func retryAfterFrom(err error) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		return apiErr.RetryAfter
	}
	return minRetryDelay
}

// isTransientFailure reports whether the error is a transport or platform
// failure (no Slack error code), which a retry may well fix.
func isTransientFailure(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		// A raw transport error from net/http.
		return true
	}
	return apiErr.Code == ""
}

func backoffFor(attempt int) time.Duration {
	d := minRetryDelay
	for i := 1; i < attempt; i++ {
		d *= 2
	}
	return d
}

// Wait blocks until every queued batch has been flushed. Used by graceful
// shutdown and by tests; it is not part of the delivery path.
func (d *Dispatcher) Wait() {
	if d == nil {
		return
	}
	d.wg.Wait()
}

// Stop refuses new work and waits for what is already queued. Cancelling the
// dispatcher's context is what makes an in-flight wait abandon early.
func (d *Dispatcher) Stop() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.stopped = true
	d.mu.Unlock()
	d.wg.Wait()
}

// Pending reports how many batches are open. Diagnostics and tests only.
func (d *Dispatcher) Pending() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.batches)
}

// RenderNotification builds the fallback text and the Block Kit body for one
// (possibly coalesced) notification. Pure, so the copy is golden-testable.
//
// Compact by design — the plan's card is "event line + issue identifier link +
// actor". A channel is a place to learn that something needs a human, and the
// issue page is where they go next.
func RenderNotification(head Message, items []Item, dropped int) (string, []Block) {
	emoji := strings.TrimSpace(head.Emoji)
	if emoji != "" {
		emoji += " "
	}
	headline := strings.TrimSpace(head.Singular)
	if len(items) > 1 {
		plural := strings.TrimSpace(head.Plural)
		if plural == "" {
			plural = headline
		}
		headline = fmt.Sprintf("%d %s", len(items)+dropped, plural)
	}

	var body strings.Builder
	body.WriteString(emoji + "*" + headline + "*")
	for _, item := range items {
		body.WriteString("\n• " + itemLine(item))
	}
	if dropped > 0 {
		body.WriteString(fmt.Sprintf("\n• …and %d more", dropped))
	}

	blocks := []Block{SectionBlock(body.String())}
	if len(items) == 1 {
		if context := singleEventContext(head); len(context) > 0 {
			blocks = append(blocks, ContextBlock(context...))
		}
	}

	// The fallback text is what Slack shows in a push notification and on
	// clients that cannot render blocks, so it carries the headline and the
	// first identifier rather than a placeholder.
	fallback := headline
	if len(items) > 0 && strings.TrimSpace(items[0].Identifier) != "" {
		fallback = headline + " — " + strings.TrimSpace(items[0].Identifier)
	}
	return fallback, ClampBlocks(blocks)
}

// itemLine renders one issue: an mrkdwn link labelled with the identifier,
// followed by the title. The URL inside the link is the canonical issue URL,
// so copying the link out of Slack yields a link that unfurls.
func itemLine(item Item) string {
	label := strings.TrimSpace(item.Identifier)
	url := strings.TrimSpace(item.URL)
	title := strings.TrimSpace(item.Title)
	switch {
	case label == "" && url == "":
		return title
	case url == "":
		if title == "" {
			return label
		}
		return "*" + label + "* " + title
	case label == "":
		label = url
	}
	line := "<" + url + "|" + label + ">"
	if title != "" {
		line += " " + title
	}
	return line
}

// singleEventContext is the small grey line under a one-event card.
func singleEventContext(head Message) []string {
	out := []string{}
	if detail := strings.TrimSpace(head.Detail); detail != "" {
		out = append(out, detail)
	}
	if actor := strings.TrimSpace(head.Actor); actor != "" {
		out = append(out, actor)
	}
	return out
}
