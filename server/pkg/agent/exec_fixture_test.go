package agent

import (
	"sync"
	"testing"
)

// Fake provider CLIs are real child processes. Letting every t.Parallel test
// launch one at once can starve their fixed protocol deadlines on CI and turn
// successful scripts into misleading initialize timeouts. Reserve a small
// process budget before writing a fixture; cleanup holds the slot until the
// owning test has reaped its child process.
var (
	testExecutableSlots = make(chan struct{}, 4)
	testExecutableUsers sync.Map
)

func reserveTestExecutableSlot(tb testing.TB) {
	tb.Helper()
	if _, loaded := testExecutableUsers.LoadOrStore(tb, struct{}{}); loaded {
		return
	}
	testExecutableSlots <- struct{}{}
	tb.Cleanup(func() {
		testExecutableUsers.Delete(tb)
		<-testExecutableSlots
	})
}
