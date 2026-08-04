package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/service"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Release-integrations dispatcher (release-hub Thread B / Phase 2-4). Subscribes
// the release lifecycle events and fans each out to the workspace's enabled
// release_integration rows whose events[] filter includes the fired event.
// Modeled on registerBitrixOutbound / registerLarkPushListeners: the actual
// delivery runs on detached, individually bounded goroutines so the publishing
// HTTP path (the comment/deploy handler) is never blocked by connector I/O.
//
// Phase 2 handled only kind="webhook"; Phase 3-4 dispatch per-kind through a
// connector registry (release_connectors.go) — each enabled matching row is
// unsealed and delivered to the connector for its kind.

// releaseOutboundTimeout bounds one outbound delivery — the same tight budget
// bitrixOutboundTimeout uses so a hung receiver can't pin a goroutine.
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
// shared enriched payload + changelog, and delivers to each matching connector
// on its own bounded goroutine. Synchronous (waits for its deliveries) so tests
// can call it directly and assert what was delivered. No-op — fail closed — when
// the seal key is unset or no integration matches.
func (h *Handler) fanOutReleaseEvent(eventType string, e events.Event) {
	box, err := releaseIntegrationBox()
	if err != nil {
		return // AGORA_RELEASE_SECRET_KEY unset → cannot decrypt secrets; fail closed
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

	// Keep only rows whose events[] filter includes the fired event and whose
	// kind has a known connector. Nothing to do → return before any enrichment.
	matched := make([]db.ReleaseIntegration, 0, len(rows))
	needBitrix := false
	for _, row := range rows {
		if !releaseIntegrationMatchesEvent(row.Events, eventType) || releaseConnectorFor(row.Kind) == nil {
			continue
		}
		matched = append(matched, row)
		if row.Kind == "bitrix" {
			needBitrix = true
		}
	}
	if len(matched) == 0 {
		return
	}

	// Shared, read-only after assembly: the enriched payload + shipped changelog.
	payload := h.buildReleasePayload(loadCtx, e, wsUUID)
	changelog := h.buildReleaseChangelog(loadCtx, eventType, e, wsUUID, needBitrix)

	var wg sync.WaitGroup
	for _, row := range matched {
		conn := releaseConnectorFor(row.Kind)
		secret, _ := openReleaseSecretRaw(box, row.SecretEncrypted) // may be empty (bitrix env fallback)
		wg.Add(1)
		go func(row db.ReleaseIntegration, conn releaseConnector, secret []byte) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), releaseOutboundTimeout)
			defer cancel()
			if err := conn(ctx, row.Config, secret, eventType, payload, changelog); err != nil {
				slog.Warn("release outbound: connector delivery failed",
					"integration_id", uuidToString(row.ID), "kind", row.Kind, "event", eventType, "error", err)
			}
		}(row, conn, secret)
	}
	wg.Wait()
}

// buildReleasePayload copies the bus event payload into a fresh map (so delivery
// goroutines only read it) and enriches it with workspace_id + resolved,
// human-readable project/sprint names the connectors render.
func (h *Handler) buildReleasePayload(ctx context.Context, e events.Event, wsUUID pgtype.UUID) map[string]any {
	payload := map[string]any{"workspace_id": e.WorkspaceID}
	if m, ok := e.Payload.(map[string]any); ok {
		for k, v := range m {
			payload[k] = v
		}
	}
	// Resolve the project name (best-effort; a missing/failed lookup leaves the
	// field absent and connectors fall back to the id/label they already have).
	if pid := payloadString(payload, "project_id"); pid != "" {
		if u, perr := util.ParseUUID(pid); perr == nil {
			if p, perr := h.Queries.GetProject(ctx, u); perr == nil && p.Title != "" {
				payload["project"] = p.Title
			}
		}
	}
	// Resolve the sprint name + branch (used for the human label and the release
	// tag/version).
	if sid := payloadString(payload, "sprint_id"); sid != "" {
		if u, perr := util.ParseUUID(sid); perr == nil {
			if sp, perr := h.Queries.GetSprint(ctx, db.GetSprintParams{ID: u, WorkspaceID: wsUUID}); perr == nil {
				if sp.Name != "" {
					payload["sprint"] = sp.Name
				}
				if _, has := payload["branch"]; !has && sp.Branch != "" {
					payload["branch"] = sp.Branch
				}
			}
		}
	}
	return payload
}

// buildReleaseChangelog assembles the shipped-issue changelog for a
// release:shipped event (empty for any other event). The base entries come from
// the sprint's readiness rollup (BuildSprintChangelog — the one definition of
// "what shipped" the QA cockpit shares); when a bitrix connector is configured,
// each entry is enriched with its linked bitrix_task_id. When the ship had no
// sprint, the bitrix path still resolves task ids straight from the payload's
// issue_ids so a single-issue ship still comments.
func (h *Handler) buildReleaseChangelog(ctx context.Context, eventType string, e events.Event, wsUUID pgtype.UUID, needBitrix bool) []releaseChangelogEntry {
	if eventType != protocol.EventReleaseShipped || h.TaskService == nil {
		return nil
	}
	var base []service.ShippedIssue
	if sid := stringFromPayload(e.Payload, "sprint_id"); sid != "" {
		if sprintUUID, perr := util.ParseUUID(sid); perr == nil {
			if cl, cerr := h.TaskService.BuildSprintChangelog(ctx, sprintUUID, wsUUID); cerr == nil {
				base = cl
			}
		}
	}
	out := make([]releaseChangelogEntry, 0, len(base))
	for _, s := range base {
		entry := releaseChangelogEntry{ID: s.ID, Identifier: s.Identifier, Title: s.Title, Verdict: s.Verdict}
		if needBitrix {
			entry.BitrixTaskID = h.loadBitrixTaskID(ctx, s.ID)
		}
		out = append(out, entry)
	}
	// No sprint changelog but Bitrix needs task ids → resolve them from the
	// payload's shipped issue_ids directly (entries carry only ID + BitrixTaskID,
	// which is all the Bitrix connector reads).
	if len(out) == 0 && needBitrix {
		for _, iid := range issueIDsFromPayload(e.Payload) {
			if tid := h.loadBitrixTaskID(ctx, iid); tid != "" {
				out = append(out, releaseChangelogEntry{ID: iid, BitrixTaskID: tid})
			}
		}
	}
	return out
}

// loadBitrixTaskID resolves an issue's linked Bitrix task id from its metadata,
// or "" when the issue isn't Bitrix-originated (or can't be loaded).
func (h *Handler) loadBitrixTaskID(ctx context.Context, issueID string) string {
	id, err := util.ParseUUID(issueID)
	if err != nil {
		return ""
	}
	issue, err := h.Queries.GetIssue(ctx, id)
	if err != nil {
		return ""
	}
	return bitrixTaskIDFromMetadata(issue.Metadata)
}

// issueIDsFromPayload extracts the release:shipped issue_ids list, tolerating
// both the in-process []string and a JSON-decoded []any shape.
func issueIDsFromPayload(payload any) []string {
	m, ok := payload.(map[string]any)
	if !ok {
		return nil
	}
	switch v := m["issue_ids"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
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

// openReleaseSecretRaw decrypts an integration's sealed secret to its plaintext
// JSON bytes for a connector to parse. Returns (nil,false) for an empty column
// (a bitrix row may legitimately have none) or an unsealable blob.
func openReleaseSecretRaw(box *secretbox.Box, sealed []byte) ([]byte, bool) {
	if len(sealed) == 0 {
		return nil, false
	}
	plain, err := box.Open(sealed)
	if err != nil {
		return nil, false
	}
	return plain, true
}
