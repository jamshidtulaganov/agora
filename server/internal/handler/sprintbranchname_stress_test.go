package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestSprintBranchName covers the sprint/<id> convention for a couple of
// sprint ids, including the invalid (zero) UUID case.
func TestSprintBranchName(t *testing.T) {
	idA := validUUID([16]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
	})
	idB := validUUID([16]byte{
		0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10,
		0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10,
	})

	tests := []struct {
		name     string
		sprintID pgtype.UUID
		want     string
	}{
		{
			name:     "first sprint id",
			sprintID: idA,
			want:     "sprint/01234567-89ab-cdef-0123-456789abcdef",
		},
		{
			name:     "second sprint id",
			sprintID: idB,
			want:     "sprint/fedcba98-7654-3210-fedc-ba9876543210",
		},
		{
			name:     "invalid id yields bare sprint/ prefix",
			sprintID: pgtype.UUID{Valid: false},
			want:     "sprint/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sprintBranchName(tt.sprintID); got != tt.want {
				t.Errorf("sprintBranchName() = %q, want %q", got, tt.want)
			}
		})
	}
}
