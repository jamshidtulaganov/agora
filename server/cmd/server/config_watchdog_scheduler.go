package main

import (
	"context"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/handler"
)

// configWatchdogInterval — how often the project-config watchdog sweeps
// risk-mapped projects for missing knowledge/QA artifacts. Hourly is plenty:
// escalation is throttled to once per 24h per project anyway; the sweep just
// bounds how stale the slog signal can get.
const configWatchdogInterval = 1 * time.Hour

// runConfigWatchdogScheduler sweeps risk-mapped projects for silently-missing
// artifacts (KB skill / qa_manifest / base suite). Always on: it only reads,
// logs, and (throttled) escalates for projects that explicitly opted into a
// risk map, so an unconfigured install does zero work.
func runConfigWatchdogScheduler(ctx context.Context, h *handler.Handler) {
	ticker := time.NewTicker(configWatchdogInterval)
	defer ticker.Stop()
	h.SweepProjectConfigWatchdog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			safeTick("config_watchdog", func() { h.SweepProjectConfigWatchdog(ctx) })
		}
	}
}
