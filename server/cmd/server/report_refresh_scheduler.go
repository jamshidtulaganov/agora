package main

import (
	"context"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/handler"
)

// reportRefreshInterval — how often due report schedules are swept. The
// tightest cadence anyone can configure is once a day at a given HH:MM, so a
// minute of granularity is already finer than the product promises; going
// lower would only add ticks that find nothing.
const reportRefreshInterval = 60 * time.Second

// runReportRefreshScheduler starts the scheduled refreshes of pinned reports
// (docs/assistant-domain-plan.md §Phase 2b). The loop itself always runs; both
// gates live inside RunDueReportSchedules and are re-read every tick —
// AGORA_ASSISTANT_SCHEDULES_ENABLED (the kill switch, flippable from Settings →
// Configs with no redeploy) and the assistant's own availability. Keeping the
// gates in the handler rather than here is what makes turning the feature off
// take effect within a minute on a running server instead of at the next boot.
func runReportRefreshScheduler(ctx context.Context, h *handler.Handler) {
	ticker := time.NewTicker(reportRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			safeTick("report_refresh", func() { h.RunDueReportSchedules(ctx) })
		}
	}
}
