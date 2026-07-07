package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// qaWatchdogInterval is how often the silent-failure watchdog sweeps. The
// run_qa fleet's p95 is ~20m, so a 5m sweep against a 30m staleness threshold
// catches a dead gate promptly without racing a slow-but-live run.
const qaWatchdogInterval = 5 * time.Minute

// qaWatchdogStaleMinutes is the age past which an unverified in_review gate is
// treated as silently dead. Env-tunable (AGORA_QA_WATCHDOG_STALE_MIN) so it can
// be set low in a test and generous in prod. Default 30.
func qaWatchdogStaleMinutes() int32 {
	if v := strings.TrimSpace(os.Getenv("AGORA_QA_WATCHDOG_STALE_MIN")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int32(n)
		}
	}
	return 30
}

// qaWatchdogWindowHours bounds escalation to RECENT gates: an issue that went
// in_review within this window but has since gone silent. Without it, the first
// sweep would mass-escalate the entire historical in_review backlog. Default 24h.
func qaWatchdogWindowHours() int32 {
	if v := strings.TrimSpace(os.Getenv("AGORA_QA_WATCHDOG_WINDOW_HOURS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return int32(n)
		}
	}
	return 24
}

// devRuntimeWaitMaxSecs is how long a dev-pinned QA task may sit queued before
// the soft-fallback reroutes it (AGORA_DEV_RUNTIME_WAIT_MAX_SECS, default 600).
func devRuntimeWaitMaxSecs() float64 {
	if v := strings.TrimSpace(os.Getenv("AGORA_DEV_RUNTIME_WAIT_MAX_SECS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return float64(n)
		}
	}
	return 600
}

// runQAWatchdogScheduler is the silent-failure SPOF guard. When auto-QA is on,
// every in_review issue is gated by run_qa — so a stale in_review issue with no
// qa:pass/qa:fail verdict and no live task is a gate that fired but produced no
// result (agent died / usage limit / never dispatched). The watchdog escalates
// those to a LOUD qa:fail + comment, so "didn't run" blocks instead of reading
// green. Only runs where AGORA_AUTO_QA_ENABLED (else there is no gate to expect).
func runQAWatchdogScheduler(ctx context.Context, queries *db.Queries, h *handler.Handler) {
	if strings.TrimSpace(os.Getenv("AGORA_AUTO_QA_ENABLED")) != "true" {
		return
	}
	ticker := time.NewTicker(qaWatchdogInterval)
	defer ticker.Stop()
	tickQAWatchdog(ctx, queries, h)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			safeTick("qa_watchdog", func() { tickQAWatchdog(ctx, queries, h) })
		}
	}
}

func tickQAWatchdog(ctx context.Context, queries *db.Queries, h *handler.Handler) {
	gates, err := queries.ListStaleUnverifiedQAGates(ctx, db.ListStaleUnverifiedQAGatesParams{
		Column1: qaWatchdogStaleMinutes(),
		Column2: qaWatchdogWindowHours(),
	})
	if err != nil {
		slog.Warn("qa watchdog: list stale gates failed", "error", err)
		return
	}
	for _, g := range gates {
		h.EscalateStaleQAGate(ctx, g.ID, g.WorkspaceID, g.Title)
	}
	// Dev-runtime pins (daemon-per-dev): queued tasks pinned to a developer's
	// daemon that went offline (or waited past the window) fall back to their
	// home runtime — unless the workspace opted into strict pinning.
	if n := h.TaskService.SweepStaleDevPinnedTasks(ctx, devRuntimeWaitMaxSecs()); n > 0 {
		slog.Info("qa watchdog: dev-pinned tasks fell back", "count", n)
	}
	if len(gates) > 0 {
		slog.Info("qa watchdog: swept stale gates", "count", len(gates))
	}
}
