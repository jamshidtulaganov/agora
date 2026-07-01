package handler

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestUUIDToString covers a valid UUID round-trip and the zero/invalid UUID case.
func TestUUIDToString(t *testing.T) {
	id := validUUID([16]byte{
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
	})

	tests := []struct {
		name string
		u    pgtype.UUID
		want string
	}{
		{
			name: "valid uuid round-trips",
			u:    id,
			want: "01234567-89ab-cdef-0123-456789abcdef",
		},
		{
			name: "zero/invalid uuid yields empty string",
			u:    pgtype.UUID{Valid: false},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uuidToString(tt.u); got != tt.want {
				t.Errorf("uuidToString() = %q, want %q", got, tt.want)
			}
		})
	}
}
