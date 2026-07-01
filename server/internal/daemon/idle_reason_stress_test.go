package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestIdleWatchdogReason(t *testing.T) {
	tests := []struct {
		name   string
		window time.Duration
		want   string
	}{
		{
			name:   "fifty milliseconds",
			window: 50 * time.Millisecond,
			want:   "agent produced no new messages for 50ms and message queue was empty; force-stopped by idle watchdog",
		},
		{
			name:   "five minutes",
			window: 5 * time.Minute,
			want:   "agent produced no new messages for 5m0s and message queue was empty; force-stopped by idle watchdog",
		},
		{
			name:   "zero duration",
			window: 0,
			want:   "agent produced no new messages for 0s and message queue was empty; force-stopped by idle watchdog",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := idleWatchdogReason(tt.window)
			if got != tt.want {
				t.Errorf("idleWatchdogReason(%v) = %q, want %q", tt.window, got, tt.want)
			}
			if !strings.Contains(got, tt.window.String()) {
				t.Errorf("idleWatchdogReason(%v) = %q, does not contain formatted window %q", tt.window, got, tt.window.String())
			}
		})
	}
}
