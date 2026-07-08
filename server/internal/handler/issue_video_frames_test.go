package handler

import (
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

// The per-issue frame-extraction lock is what keeps the on-assign background
// kick and the claim-time synchronous pass from running ffmpeg on the same
// video concurrently (which would double-append frames to the description
// before either sets the idempotency flag). These tests pin that contract.

func TestTryLockIssueFrames_MutualExclusion(t *testing.T) {
	id := util.MustParseUUID("11111111-1111-4111-8111-111111111111")

	// First acquire (as the blocking background kick would) succeeds.
	unlock := lockIssueFrames(id)

	// The claim path must NOT block while an extraction is in flight — TryLock
	// returns false so the claim proceeds on the current description.
	if _, ok := tryLockIssueFrames(id); ok {
		t.Fatal("tryLockIssueFrames acquired a held lock — the claim would run a second concurrent ffmpeg pass")
	}

	// A different issue is independently lockable — the lock is per-issue.
	other := util.MustParseUUID("22222222-2222-4222-8222-222222222222")
	otherUnlock, ok := tryLockIssueFrames(other)
	if !ok {
		t.Fatal("tryLockIssueFrames failed for an unrelated issue — the lock must not be global")
	}
	otherUnlock()

	// Once released, the claim path can acquire it again.
	unlock()
	reUnlock, ok := tryLockIssueFrames(id)
	if !ok {
		t.Fatal("tryLockIssueFrames failed after the lock was released")
	}
	reUnlock()
}

func TestTryLockIssueFrames_ReleaseUnblocksBlockingLock(t *testing.T) {
	id := util.MustParseUUID("33333333-3333-4333-8333-333333333333")

	unlock, ok := tryLockIssueFrames(id)
	if !ok {
		t.Fatal("first tryLockIssueFrames should acquire a free lock")
	}

	acquired := make(chan struct{})
	go func() {
		// The background kick uses the blocking acquire; it should proceed only
		// after the claim-time holder releases.
		release := lockIssueFrames(id)
		close(acquired)
		release()
	}()

	select {
	case <-acquired:
		t.Fatal("blocking lock acquired while the lock was still held")
	default:
	}

	unlock()
	<-acquired // must now unblock
}
