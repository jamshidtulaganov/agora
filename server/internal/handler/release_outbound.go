package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/integrations/releasehook"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Release-integrations dispatcher (release-hub Thread B / Phase 2). Subscribes
// the release lifecycle events and fans each out to the workspace's enabled
// release_integration rows whose events[] filter includes the fired event.
// Modeled on registerBitrixOutbound / registerLarkPushListeners: the actual
// delivery runs on detached, individually bounded goroutines so the publishing
// HTTP path (the comment/deploy handler) is never blocked by webhook I/O.

// releaseOutboundTimeout bounds one outbound webhook delivery — the same tight
// budget bitrixOutboundTimeout uses so a hung receiver can't pin a goroutine.
const releaseOutboundTimeout = 10 * time.Second

// releaseEventShortName maps a bus event constant to the short name stored in
// release_integration.events[]. Returns "" for a non-release event.
func releaseEventShortName(eventType string) string {
	switch eventType {
	case protocol.EventDeployRecorded:
		return releaseEventDeployRecorded
	case protocol.EventReleaseShipped:
		return releaseEventReleaseShipped
	default:
		return ""
	}
}

// releaseIntegrationMatchesEvent reports whether an integration's events[]
// filter includes the fired bus event. Pure — unit-tested without a DB.
func releaseIntegrationMatchesEvent(filter []string, eventType string) bool {
	short := releaseEventShortName(eventType)
	if short == "" {
		return false
	}
	for _, e := range filter {
		if e == short {
			return true
		}
	}
	return false
}

// DispatchReleaseEvent is the bus subscriber's entry point. It returns
// immediately after spawning the fan-out so the publishing goroutine is not
// blocked. No-op when this isn't a release event or the workspace is unknown.
func (h *Handler) DispatchReleaseEvent(eventType string, e events.Event) {
	if h.Queries == nil || e.WorkspaceID == "" {
		return
	}
	if releaseEventShortName(eventType) == "" {
		return
	}
	go h.fanOutReleaseEvent(eventType, e)
}

// fanOutReleaseEvent loads the workspace's enabled integrations, assembles the
// shared payload, and delivers to each matching webhook on its own bounded
// goroutine. Synchronous (waits for its deliveries) so tests can call it
// directly and assert what was delivered. No-op — fail closed — when the seal
// key is unset or no integration matches.
func (h *Handler) fanOutReleaseEvent(eventType string, e events.Event) {
	box, err := releaseIntegrationBox()
	if err != nil {
		return // AGORA_RELEASE_SECRET_KEY unset → cannot decrypt URLs; fail closed
	}
	wsUUID, err := util.ParseUUID(e.WorkspaceID)
	if err != nil {
		return
	}
	loadCtx, cancel := context.WithTimeout(context.Background(), releaseOutboundTimeout)
	defer cancel()
	rows, err := h.Queries.ListEnabledReleaseIntegrationsByWorkspace(loadCtx, wsUUID)
	if err != nil || len(rows) == 0 {
		return
	}

	// Shared body: {event, workspace_id, ...payload}. Read-only after assembly,
	// so all delivery goroutines can marshal it concurrently.
	body := map[string]any{"event": eventType, "workspace_id": e.WorkspaceID}
	if m, ok := e.Payload.(map[string]any); ok {
		for k, v := range m {
			body[k] = v
		}
	}
	// release:shipped carries the sprint's changelog for release notes.
	if eventType == protocol.EventReleaseShipped && h.TaskService != nil {
		if sid := stringFromPayload(e.Payload, "sprint_id"); sid != "" {
			if sprintUUID, perr := util.ParseUUID(sid); perr == nil {
				if changelog, cerr := h.TaskService.BuildSprintChangelog(loadCtx, sprintUUID, wsUUID); cerr == nil && len(changelog) > 0 {
					body["changelog"] = toChangelogEntries(changelog)
				}
			}
		}
	}

	var wg sync.WaitGroup
	for _, row := range rows {
		if row.Kind != "webhook" || !releaseIntegrationMatchesEvent(row.Events, eventType) {
			continue
		}
		secret, ok := openWebhookSecret(box, row.SecretEncrypted)
		if !ok || secret.URL == "" {
			slog.Warn("release outbound: unsealable webhook secret", "integration_id", uuidToString(row.ID))
			continue
		}
		wg.Add(1)
		go func(id, url, signing string) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), releaseOutboundTimeout)
			defer cancel()
			if err := releaseHookClient.Deliver(ctx, url, signing, eventType, body); err != nil {
				slog.Warn("release outbound: webhook delivery failed", "integration_id", id, "event", eventType, "error", err)
			}
		}(uuidToString(row.ID), secret.URL, secret.Signing)
	}
	wg.Wait()
}

// stringFromPayload extracts a string field from a map[string]any bus payload.
func stringFromPayload(payload any, key string) string {
	m, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// toChangelogEntries maps the service's shipped-issue rows to the webhook
// changelog shape (the two structs are intentionally separate so the service
// stays free of any integrations/ coupling).
func toChangelogEntries(in []service.ShippedIssue) []releasehook.ChangelogEntry {
	out := make([]releasehook.ChangelogEntry, len(in))
	for i, s := range in {
		out[i] = releasehook.ChangelogEntry{Identifier: s.Identifier, Title: s.Title, Verdict: s.Verdict}
	}
	return out
}
