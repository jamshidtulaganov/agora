package assistant

import (
	"context"
	"strings"
	"time"
)

// Per-run context carried into the tool executor.
//
// The executor's signature is deliberately narrow (userID, sessionID, tool,
// args) because those four are INPUTS a tool acts on. A timezone is not — it is
// ambient request context, the same class of thing as a trace id, and threading
// it through every tool signature would be forty edits for something only three
// analytics tools read.
//
// It matters because "today" is not a server-side fact. A run started at 23:50
// in Asia/Dubai is asking about a day that UTC says has not begun, and an
// activity digest that answers in UTC silently drops the caller's whole
// evening. The run captures the browser's timezone per message
// (assistant_run.context_timezone, migration 197); this is how it reaches the
// query that needs it.

type runTimezoneKey struct{}

// WithTimezone carries the caller's IANA timezone into a tool execution.
func WithTimezone(ctx context.Context, tz string) context.Context {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return ctx
	}
	return context.WithValue(ctx, runTimezoneKey{}, tz)
}

// TimezoneFrom returns the timezone captured for this run, or "" when the run
// carried none (an older client, or a tool executed outside a run). Callers
// fall back to the user's profile timezone and then to UTC — never to the
// server's local zone, which is an accident of deployment.
func TimezoneFrom(ctx context.Context) string {
	tz, _ := ctx.Value(runTimezoneKey{}).(string)
	return tz
}

// LoadLocation resolves an IANA name, falling back to UTC. A stored timezone
// the platform does not know (a renamed zone, a typo that predates validation)
// must degrade to a defined answer rather than fail a read tool.
func LoadLocation(tz string) *time.Location {
	if tz = strings.TrimSpace(tz); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil && loc != nil {
			return loc
		}
	}
	return time.UTC
}

// DayWindow is the [from, to] instant range for "the last n days" as the
// CALLER's calendar reads it: n whole local days ending with the one in
// progress. n = 1 is "today", and an issue filed at 23:50 local is inside it
// however far the UTC date has already moved on.
func DayWindow(now time.Time, loc *time.Location, days int) (time.Time, time.Time) {
	if days < 1 {
		days = 1
	}
	local := now.In(loc)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return startOfToday.AddDate(0, 0, -(days - 1)), local
}
