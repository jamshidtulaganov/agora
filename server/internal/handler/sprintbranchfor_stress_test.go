package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// validUUID builds a valid pgtype.UUID from 16 raw bytes so the fallback
// branch has a deterministic, non-zero id to render.
func validUUID(b [16]byte) pgtype.UUID {
	return pgtype.UUID{Bytes: b, Valid: true}
}

// TestSprintBranchFor covers the two branches of SprintBranchFor: an explicit
// sprint branch (returned verbatim after trimming) vs the sprint/<id> fallback
// when the branch is effectively empty.
func TestSprintBranchFor(t *testing.T) {
	// A fixed, valid id → known hex string via UUIDToString.
	id := validUUID([16]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
	})
	const idStr = "01234567-89ab-cdef-0123-456789abcdef"

	tests := []struct {
		name   string
		sprint db.Sprint
		want   string
	}{
		{
			name:   "explicit branch wins over fallback",
			sprint: db.Sprint{ID: id, Branch: "billing"},
			want:   "billing",
		},
		{
			name:   "surrounding whitespace is trimmed off the returned branch",
			sprint: db.Sprint{ID: id, Branch: "  sprint-9  "},
			want:   "sprint-9",
		},
		{
			name:   "casing and slug-like chars are preserved verbatim, not normalized",
			sprint: db.Sprint{ID: id, Branch: "Feature/Billing_V2"},
			want:   "Feature/Billing_V2",
		},
		{
			name:   "empty branch falls back to sprint/<id>",
			sprint: db.Sprint{ID: id, Branch: ""},
			want:   "sprint/" + idStr,
		},
		{
			name:   "whitespace-only branch is treated as empty and falls back",
			sprint: db.Sprint{ID: id, Branch: "   \t\n  "},
			want:   "sprint/" + idStr,
		},
		{
			name:   "empty branch with invalid id yields the bare sprint/ prefix",
			sprint: db.Sprint{ID: pgtype.UUID{Valid: false}, Branch: ""},
			want:   "sprint/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SprintBranchFor(tt.sprint); got != tt.want {
				t.Errorf("SprintBranchFor() = %q, want %q", got, tt.want)
			}
		})
	}
}
