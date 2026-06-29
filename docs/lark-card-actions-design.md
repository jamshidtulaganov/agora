# Lark Interactive Card Actions — Design

Goal: turn Lark cards from read-only notifications into a **control surface**. A
member taps a button on a card (Mark In Review · QA ✅/❌ · Assign to me) and the
Agora issue mutates, with the card updating in place — without leaving Lark.

Status: **design (plan-first); build pending review.** Grounded in the real
code as of commit `f297312b`.

## How card actions arrive

When a user taps a `"type":"request"` button on an interactive card, Lark
delivers a `card.action.trigger` event over the **same long-conn** the bot
already holds (no new transport). Today the frame decoder drops it:

- `ws_frame_decoder.go:57` — `if env.Header.EventType != "im.message.receive_v1" { return ..., false, nil }` — every non-message event (including `card.action.trigger`) is silently dropped + ACKed 200.

Event shape (schema 2.0):
```
header.event_type = "card.action.trigger"
event = {
  operator: { open_id },          // who tapped — must be a bound member
  token,                          // callback token
  action: { tag, value: {...} },  // value = the JSON we put on the button
  context: { open_message_id, open_chat_id }
}
```

## Response strategy — two options

A card-action callback expects a response. Two ways on long-conn:

1. **Inline ACK response** — put `{"toast": {...}, "card": {...}}` in the ACK
   frame's `data` field (today `NewAckFrame` writes `{"code":200,"headers":null,"data":null}`, `ws_frame.go:375`). Lark shows the toast + swaps the card instantly. **Exact shape is unverified** against this long-conn build — this is the live-test risk.
2. **ACK 200 + async patch** *(recommended first cut)* — ACK 200 immediately, then mutate the issue and patch the card via the existing `APIClient.PatchInteractiveCard` (`client.go:36`) targeting `context.open_message_id`. No uncertain inline shape; reuses a proven API. Trade-off: a ~1s gap before the card updates, and no inline toast.

Ship option 2 first; upgrade to option 1's toast once the response shape is confirmed live.

## Files to change

1. **`types.go`** — new `CardAction` struct: `{ EventID, AppID, OperatorOpenID OpenID, ChatID ChatID, MessageID string /*open_message_id*/, Value map[string]string }`.

2. **`ws_frame_decoder.go`** — add `DecodeCardAction(payload []byte, inst db.LarkInstallation) (CardAction, bool, error)`. Keep `Decode` (messages) untouched; the connector tries `DecodeCardAction` when `Decode` returns `(zero,false,nil)` and `header.event_type == "card.action.trigger"`.

3. **`ws_connector.go`** — add a `CardActionHandler` seam to `WSConnectorConfig` (mirrors the `Dispatcher` field). In the frame loop (the ACK branch near line 398), route a decoded card action to `CardActionHandler.Handle(...)` → ACK 200 (option 2). Errors log + ACK 200 (a bad action must not retry-storm).

4. **new `card_action.go`** — `CardActionHandler.Handle(ctx, inst, action CardAction)`:
   - **Identity gate**: `GetLarkUserBindingByOpenID(inst.ID, action.OperatorOpenID)` — only a bound workspace member may mutate. Unbound → no-op (or a "bind first" toast).
   - **Parse** `action.Value`: `{ "action": "set_status"|"qa_pass"|"assign_me", "issue_id": "...", "status": "in_review" }`.
   - **Mutate** the issue (see the gap below), attributing the bound member as actor.
   - **Re-render** the issue card with new state + `PatchInteractiveCard(open_message_id, newCard)`.

5. **new action-card builder** (extend `notify.go`) — `IssueActionCard(issue, member, actions...)` → card JSON with `"type":"request"` buttons carrying `value` payloads, plus an `Open in Agora` URL button. Patched version greys out the just-taken action.

6. **`cmd/server/router.go`** — construct `CardActionHandler` (needs `Queries`, `IssueService`/status-update path, `APIClient`, `InstallationService`) in the Lark wiring block and pass it into `buildLarkConnectorFactory` → `WSConnectorConfig`.

## ⚠️ The mutation gap (decide before building)

`IssueService` exposes `Create` but **no `UpdateStatus`/`Update`** (`service/issue.go` — only `Create` + helpers). Issue status changes today happen in the HTTP handler (`handler/issue.go`) calling `Queries.Update*` directly + publishing `EventIssueUpdated`. So a card action can't just call a service method.

Options:
- **(a) Extract** `IssueService.UpdateStatus(ctx, issueID, status, actorType, actorID)` that does the `Queries.Update` + `broadcastIssueUpdated` + `maybeEnqueueOnAssign`, then call it from BOTH the HTTP handler and the card-action handler. Cleanest; matches the `Create` precedent (one transport-agnostic entry point). **Recommended.**
- (b) Replicate the handler's update+publish inside `card_action.go`. Faster but duplicates logic and risks drift (the exact anti-pattern CLAUDE.md warns against).

Pick (a).

## Scopes + dev-console

- `im:message` (send + patch cards) — already required for replies.
- The Lark app must have **message-card callback enabled** and `card.action.trigger` in its long-conn **event subscription** (dev console). Confirm the PersonalAgent (scan-installed) archetype can subscribe to it — this is an open risk alongside the existing union_id/long-conn-archetype questions.

## Build order (vertical slice → live-test)

1. `types.go` + `DecodeCardAction` — connector stops dropping `card.action.trigger` (compiles; safe).
2. `IssueService.UpdateStatus` extraction (option a) + reuse in the HTTP handler (no behavior change, covered by existing handler tests).
3. `CardActionHandler` with ONE action (`set_status → in_review`) + async `PatchInteractiveCard`.
4. `IssueActionCard` builder + a way to post it (e.g. attach buttons to the issue-created confirmation, or a `/card <id>` command).
5. **Live-test** against the bound `sd-bridge-lead` bot: post an action card to the DM, tap the button, watch logs for the `card.action.trigger` event + the patch. If the button shows a stuck spinner, switch to option 1 (inline ACK `data` with `{toast, card}`) and re-test.

## Out of scope (later)
- Inline ACK toast (option 1) once the response shape is confirmed.
- Multi-action cards (assign picker, status dropdown via `select_static`).
- Group-card actions (the operator's open_id → member resolution is the same; routing differs only in chat scope).
