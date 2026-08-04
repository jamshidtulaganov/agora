package main

import (
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/handler"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// registerReleaseOutbound subscribes the release-lifecycle events and fans each
// out to the workspace's configured release integrations (release-hub Thread B
// / Phase 2). Like registerLarkPushListeners it hangs off the SAME bus the WS
// broadcaster uses, so it covers every deploy/ship seam without a parallel
// notification path. The work runs on detached goroutines inside
// DispatchReleaseEvent, so these subscribers never sit on the request path.
//
// No-op unless AGORA_RELEASE_SECRET_KEY is set AND the workspace has an enabled
// integration — the dispatcher fails closed on an unset key and returns early
// when no row matches, so a deployment without any release integration pays
// nothing.
func registerReleaseOutbound(bus *events.Bus, h *handler.Handler) {
	if bus == nil || h == nil {
		return
	}
	bus.Subscribe(protocol.EventDeployRecorded, func(e events.Event) {
		h.DispatchReleaseEvent(protocol.EventDeployRecorded, e)
	})
	bus.Subscribe(protocol.EventReleaseShipped, func(e events.Event) {
		h.DispatchReleaseEvent(protocol.EventReleaseShipped, e)
	})
}
