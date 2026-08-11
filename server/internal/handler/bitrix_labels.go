package handler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/integrations/bitrix"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

const bitrixLabelNameMaxLen = 64

// syncBitrixTagsAsLabels mirrors Bitrix task tags onto Agora issue labels.
// Alias spellings collapse to a canonical name. Type-class tags (bug/feature
// and their RU/UZ synonyms) are skipped here — intake triage (AI) owns
// type:bug|feature|question from title/description/media, because portal tags
// are too noisy to trust. Best-effort and additive — never detaches labels a
// human may have added in Agora.
func (h *Handler) syncBitrixTagsAsLabels(ctx context.Context, wsID, issueID pgtype.UUID, task *bitrix.Task) {
	if task == nil || len(task.Tags) == 0 {
		return
	}
	names := bitrixTagsToLabelNames(task.Tags)
	if len(names) == 0 {
		return
	}
	for _, name := range names {
		if err := h.ensureAndAttachBitrixLabel(ctx, wsID, issueID, name); err != nil {
			slog.Warn("bitrix sync: attach tag label failed",
				"issue_id", util.UUIDToString(issueID),
				"task_id", task.ID, "label", name, "error", err)
		}
	}
}

func bitrixTagsToLabelNames(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, raw := range tags {
		name := bitrixTagToLabelName(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// canonicalizeBitrixTag returns the alias-table canonical name when the tag
// belongs to a known synonym group; otherwise the normalized spelling itself.
func canonicalizeBitrixTag(tag string) string {
	want := normalizeTag(tag)
	if want == "" {
		return ""
	}
	for canonical, spellings := range bitrixTagAliases() {
		if normalizeTag(canonical) == want {
			return normalizeTag(canonical)
		}
		for _, s := range spellings {
			if normalizeTag(s) == want {
				return normalizeTag(canonical)
			}
		}
	}
	return want
}

// bitrixTagToLabelName maps a Bitrix tag onto the Agora label name we attach.
// Returns "" for type-class aliases (bug/feature) so the triage agent decides
// type:*; everything else uses the canonical (or raw normalized) tag string.
func bitrixTagToLabelName(tag string) string {
	canonical := canonicalizeBitrixTag(tag)
	if canonical == "" {
		return ""
	}
	switch canonical {
	case "bug", "feature":
		return ""
	}
	name := canonical
	if utf8.RuneCountInString(name) > bitrixLabelNameMaxLen {
		name = string([]rune(name)[:bitrixLabelNameMaxLen])
	}
	return name
}

func bitrixLabelColor(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "type:bug":
		return "#ef4444"
	case "type:feature":
		return "#2563eb"
	case "server", "task":
		return "#6b7280"
	default:
		return "#64748b"
	}
}

func (h *Handler) ensureAndAttachBitrixLabel(ctx context.Context, wsID, issueID pgtype.UUID, name string) error {
	labelID, err := h.ensureBitrixLabel(ctx, wsID, name)
	if err != nil {
		return err
	}
	return h.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID:     issueID,
		LabelID:     labelID,
		WorkspaceID: wsID,
	})
}

func (h *Handler) ensureBitrixLabel(ctx context.Context, wsID pgtype.UUID, name string) (pgtype.UUID, error) {
	existing, err := h.Queries.GetLabelByName(ctx, db.GetLabelByNameParams{
		WorkspaceID: wsID,
		Name:        name,
	})
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		// Case-insensitive fallback: workspace may already have "Bug" etc.
		if id, ok := h.findBitrixLabelCI(ctx, wsID, name); ok {
			return id, nil
		}
		return pgtype.UUID{}, err
	}
	created, cerr := h.Queries.CreateLabel(ctx, db.CreateLabelParams{
		WorkspaceID: wsID,
		Name:        name,
		Color:       bitrixLabelColor(name),
	})
	if cerr == nil {
		return created.ID, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(cerr, &pgErr) && pgErr.Code == "23505" {
		again, gerr := h.Queries.GetLabelByName(ctx, db.GetLabelByNameParams{
			WorkspaceID: wsID,
			Name:        name,
		})
		if gerr == nil {
			return again.ID, nil
		}
		if id, ok := h.findBitrixLabelCI(ctx, wsID, name); ok {
			return id, nil
		}
	}
	return pgtype.UUID{}, cerr
}

func (h *Handler) findBitrixLabelCI(ctx context.Context, wsID pgtype.UUID, name string) (pgtype.UUID, bool) {
	labels, err := h.Queries.ListLabels(ctx, wsID)
	if err != nil {
		return pgtype.UUID{}, false
	}
	for _, l := range labels {
		if strings.EqualFold(l.Name, name) {
			return l.ID, true
		}
	}
	return pgtype.UUID{}, false
}
