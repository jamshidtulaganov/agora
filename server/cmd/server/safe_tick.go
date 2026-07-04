package main

import (
	"log/slog"
	"runtime/debug"
)

// safeTick runs a scheduler's per-tick function with panic recovery. A periodic
// scheduler goroutine that panics inside a tick dies PERMANENTLY for the process
// lifetime — silently taking a safety mechanism (the QA silent-gate watchdog,
// the sprint-end merge gate, the config/knowledge watchdog, the Bitrix poll)
// offline with no error surfaced. Recovering keeps the ticker alive so the next
// tick runs normally. name identifies the scheduler in the recovery log.
func safeTick(name string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("recovered panic in scheduler tick",
				"scheduler", name, "panic", rec, "stack", string(debug.Stack()))
		}
	}()
	fn()
}
