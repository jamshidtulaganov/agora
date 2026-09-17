package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// The assistant's SETTINGS tools — the user's own preferences rather than a
// workspace's data.
//
// Three things make this file different from every other executor file, and
// each one is deliberate:
//
//  1. Most of these tools are USER-scoped. update_sidebar and
//     update_my_settings take no workspace at all, so they invoke their handler
//     with workspaceID == "" — the same thing the router does for /api/me,
//     which mounts no workspace middleware. There is nothing to gate: the only
//     row any of them can touch is the caller's own, and the caller is resolved
//     from X-User-ID before dispatch. The two workspace-scoped ones
//     (update_notification_preferences, and get_my_settings when it is asked
//     for a specific workspace) resolve membership FIRST, like every other
//     workspace tool here.
//
//  2. The writes go through the REAL handlers, for the same reason the rest of
//     assistant_writes.go does: UpdateMe owns language validation, timezone
//     validation, the hidden-nav normalizer, and the rule that Settings can
//     never be hidden. Re-implementing any of that here would produce an
//     assistant whose settings behave differently from the same switch in the
//     UI, and would drift the next time one of those rules changes.
//
//  3. They MERGE. hidden_nav and the notification map are both whole-value
//     columns: PATCHing one with "hide usage" would erase everything else the
//     user hides. So each write reads the current value, applies the named
//     change to it, and sends the merged result. That is also why the results
//     below report `hidden`/`shown`/`changed` separately from the final state —
//     the model has to be able to say what moved, not just what is true now.

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// assistantHiddenNav decodes the user's hidden-nav column. A malformed blob
// degrades to "hides nothing" rather than failing the tool — the same call
// userToResponse makes for /api/me.
func assistantHiddenNav(raw []byte) []string {
	keys := []string{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &keys); err != nil {
			return []string{}
		}
	}
	if keys == nil {
		keys = []string{}
	}
	return keys
}

// assistantNavKeys trims and drops blanks from a model-supplied key list,
// preserving order. Shape validation (the key pattern, the always-visible
// list, the length cap) belongs to normalizeHiddenNav, which the PATCH runs
// through — this only removes noise the model would not mean.
func assistantNavKeys(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, key := range raw {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// assistantNotificationGroups and assistantNotificationValues are the accepted
// vocabularies, read from the endpoint's OWN allowlists rather than copied, so
// a group or value added to the product is one the assistant can name without
// a second edit here — and one it can never name wrongly.
func assistantNotificationGroups() []string { return sortedAllowlist(validNotifGroups) }

func assistantNotificationValues() []string { return sortedAllowlist(validNotifValues) }

func sortedAllowlist(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// get_my_settings
// ---------------------------------------------------------------------------

type assistantGetMySettingsArgs struct {
	WorkspaceID string `json:"workspace_id"`
}

func (h *Handler) assistantGetMySettings(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantGetMySettingsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	// An explicit argument wins; otherwise the workspace this message was sent
	// from. Neither is guessed: with no focus and no argument the notification
	// half of the answer is reported as unknown rather than filled in from
	// whichever workspace happens to be first.
	target := strings.TrimSpace(args.WorkspaceID)
	if target == "" {
		target = assistant.FocusWorkspaceFrom(ctx)
	}

	var ws db.Workspace
	scoped := false
	if target != "" {
		resolved, _, err := h.assistantMembership(ctx, caller.UUID, target)
		if err != nil {
			return nil, err
		}
		ws = resolved
		scoped = true
	}

	user, err := h.Queries.GetUser(ctx, caller.UUID)
	if err != nil {
		return nil, errors.New("could not read your settings")
	}

	out := map[string]any{
		"name":                user.Name,
		"email":               user.Email,
		"language":            textToPtr(user.Language),
		"timezone":            textToPtr(user.Timezone),
		"hidden_nav":          assistantHiddenNav(user.HiddenNav),
		"notification_groups": assistantNotificationGroups(),
		"notification_values": assistantNotificationValues(),
	}
	if !scoped {
		out["workspace"] = nil
		out["notification_preferences"] = nil
		out["notification_preferences_note"] = "notification preferences are per workspace and no workspace is in focus for this message — call again with workspace_id to read one"
		return json.Marshal(out)
	}

	prefs, err := h.assistantReadNotificationPreferences(ctx, caller.UUID, ws.ID)
	if err != nil {
		return nil, errors.New("could not read your notification preferences")
	}
	out["workspace"] = map[string]any{
		"id":   uuidToString(ws.ID),
		"slug": ws.Slug,
		"name": ws.Name,
	}
	out["notification_preferences"] = prefs
	out["notification_preferences_note"] = "a group missing from this map is on its default, which is \"all\""
	return json.Marshal(out)
}

// assistantReadNotificationPreferences returns the stored map for one
// workspace, or an empty map when the user has never changed anything there —
// an absent row means "everything on its default", not an error.
func (h *Handler) assistantReadNotificationPreferences(ctx context.Context, userUUID, workspaceUUID pgtype.UUID) (map[string]string, error) {
	pref, err := h.Queries.GetNotificationPreference(ctx, db.GetNotificationPreferenceParams{
		WorkspaceID: workspaceUUID,
		UserID:      userUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	prefs := map[string]string{}
	if uerr := json.Unmarshal(pref.Preferences, &prefs); uerr != nil {
		return map[string]string{}, nil
	}
	return prefs, nil
}

// ---------------------------------------------------------------------------
// update_my_settings
// ---------------------------------------------------------------------------

type assistantUpdateMySettingsArgs struct {
	Language *string `json:"language"`
	Timezone *string `json:"timezone"`
	Name     *string `json:"name"`
}

func (h *Handler) assistantUpdateMySettings(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateMySettingsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}

	// Pointers, not values: "" is a meaningful timezone (clear the pin), so
	// absent and empty cannot be the same thing.
	patch := map[string]any{}
	changed := []string{}
	if args.Language != nil {
		patch["language"] = *args.Language
		changed = append(changed, "language")
	}
	if args.Timezone != nil {
		patch["timezone"] = *args.Timezone
		changed = append(changed, "timezone")
	}
	if args.Name != nil {
		patch["name"] = *args.Name
		changed = append(changed, "name")
	}
	if len(patch) == 0 {
		return nil, errors.New("name at least one setting to change: language, timezone or name")
	}

	body, err := json.Marshal(patch)
	if err != nil {
		return nil, errors.New("could not build the settings update")
	}
	// UpdateMe owns the validation — the supported-language list, the IANA
	// lookup, the non-empty name rule — and its message is the correction the
	// model needs, so it is relayed rather than restated.
	status, respBody := h.assistantInvoke(ctx, h.UpdateMe, http.MethodPatch, "/api/me",
		caller.ID, "", string(body), nil)
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update your settings")
	}

	var updated UserResponse
	_ = json.Unmarshal(respBody, &updated)
	return json.Marshal(map[string]any{
		"updated":  changed,
		"name":     updated.Name,
		"language": updated.Language,
		"timezone": updated.Timezone,
	})
}

// ---------------------------------------------------------------------------
// update_sidebar
// ---------------------------------------------------------------------------

type assistantUpdateSidebarArgs struct {
	Hide []string `json:"hide"`
	Show []string `json:"show"`
}

func (h *Handler) assistantUpdateSidebar(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateSidebarArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	hide := assistantNavKeys(args.Hide)
	show := assistantNavKeys(args.Show)
	if len(hide) == 0 && len(show) == 0 {
		return nil, errors.New("name at least one sidebar item to hide or to show")
	}
	// Both at once is not a merge the user could have meant, and picking a
	// winner silently would be the wrong half half the time.
	for _, key := range hide {
		for _, other := range show {
			if key == other {
				return nil, errors.New("\"" + key + "\" is in both hide and show — ask which one they meant")
			}
		}
	}

	user, err := h.Queries.GetUser(ctx, caller.UUID)
	if err != nil {
		return nil, errors.New("could not read your sidebar settings")
	}
	current := assistantHiddenNav(user.HiddenNav)

	dropping := map[string]bool{}
	for _, key := range show {
		dropping[key] = true
	}
	next := make([]string, 0, len(current)+len(hide))
	kept := map[string]bool{}
	shown := []string{}
	for _, key := range current {
		if dropping[key] {
			shown = append(shown, key)
			continue
		}
		if kept[key] {
			continue
		}
		kept[key] = true
		next = append(next, key)
	}
	hidden := []string{}
	for _, key := range hide {
		if kept[key] {
			continue // already hidden — a no-op, not a change to report
		}
		kept[key] = true
		next = append(next, key)
		hidden = append(hidden, key)
	}

	body, err := json.Marshal(map[string]any{"hidden_nav": next})
	if err != nil {
		return nil, errors.New("could not build the sidebar update")
	}
	// normalizeHiddenNav inside UpdateMe is what refuses "settings" and any
	// malformed key, and its wording ("nav item cannot be hidden: settings")
	// is the answer the user should hear.
	status, respBody := h.assistantInvoke(ctx, h.UpdateMe, http.MethodPatch, "/api/me",
		caller.ID, "", string(body), nil)
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update your sidebar")
	}

	var updated UserResponse
	_ = json.Unmarshal(respBody, &updated)
	stored := updated.HiddenNav
	if stored == nil {
		stored = []string{}
	}
	return json.Marshal(map[string]any{
		"hidden":     hidden,
		"shown":      shown,
		"hidden_nav": stored,
	})
}

// ---------------------------------------------------------------------------
// update_notification_preferences
// ---------------------------------------------------------------------------

type assistantUpdateNotificationPreferencesArgs struct {
	WorkspaceID string            `json:"workspace_id"`
	Preferences map[string]string `json:"preferences"`
}

func (h *Handler) assistantUpdateNotificationPreferences(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	var args assistantUpdateNotificationPreferencesArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, errAssistantBadArgs
	}
	// Membership before argument validation, so an outsider hears the
	// membership refusal rather than a hint that the workspace exists.
	ws, _, err := h.assistantMembership(ctx, caller.UUID, strings.TrimSpace(args.WorkspaceID))
	if err != nil {
		return nil, err
	}
	if len(args.Preferences) == 0 {
		return nil, errors.New("name at least one notification group to change, e.g. {\"comments\":\"muted\"}")
	}

	// PUT replaces the whole map, so the request has to carry the groups the
	// user did not mention. Stored groups the endpoint no longer recognises
	// are dropped rather than sent back into its validator — otherwise one
	// retired group would freeze every later change.
	stored, err := h.assistantReadNotificationPreferences(ctx, caller.UUID, ws.ID)
	if err != nil {
		return nil, errors.New("could not read your current notification preferences")
	}
	merged := map[string]string{}
	for group, value := range stored {
		if validNotifGroups[group] && validNotifValues[value] {
			merged[group] = value
		}
	}
	changed := map[string]any{}
	for group, value := range args.Preferences {
		// Unvalidated on purpose: UpdateNotificationPreferences owns the group
		// and value allowlists, and its 400 is the correction the model needs.
		merged[group] = value
		previous, had := stored[group]
		if !had {
			previous = ""
		}
		changed[group] = map[string]any{"from": previous, "to": value}
	}

	body, err := json.Marshal(map[string]any{"preferences": merged})
	if err != nil {
		return nil, errors.New("could not build the notification update")
	}
	status, respBody := h.assistantInvoke(ctx, h.UpdateNotificationPreferences, http.MethodPut,
		"/api/notification-preferences", caller.ID, uuidToString(ws.ID), string(body), nil)
	if status != http.StatusOK {
		return nil, assistantHandlerError(status, respBody, "could not update your notification preferences")
	}

	var updated struct {
		Preferences map[string]string `json:"preferences"`
	}
	_ = json.Unmarshal(respBody, &updated)
	if updated.Preferences == nil {
		updated.Preferences = map[string]string{}
	}
	return json.Marshal(map[string]any{
		"workspace_id":   uuidToString(ws.ID),
		"workspace_slug": ws.Slug,
		"changed":        changed,
		"preferences":    updated.Preferences,
	})
}
