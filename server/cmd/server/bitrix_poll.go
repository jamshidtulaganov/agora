package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/handler"
)

// bitrixSyncPollInterval is the period for the Bitrix safety-net poll, from
// BITRIX_SYNC_POLL_INTERVAL (a Go duration, e.g. "5m"). Empty / invalid / <= 0
// disables the poll — webhooks then carry sync alone.
func bitrixSyncPollInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv("BITRIX_SYNC_POLL_INTERVAL"))
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// runBitrixSyncPoll periodically re-syncs active tracked Bitrix tasks so an
// imported issue's status / stage / comments / attachments stay live even if a
// webhook was missed or never registered (the "always sync" backbone). No-op
// when the interval is unset.
func runBitrixSyncPoll(ctx context.Context, h *handler.Handler) {
	interval := bitrixSyncPollInterval()
	if interval <= 0 {
		slog.Info("bitrix sync poll: disabled (set BITRIX_SYNC_POLL_INTERVAL to enable)")
		return
	}
	slog.Info("bitrix sync poll: starting", "interval", interval.String())
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.PollBitrixActiveTasks(ctx)
		}
	}
}
