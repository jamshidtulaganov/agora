package handler

import "testing"

// TestParseSprintDate covers the sprint date parser — it must accept the
// date-only "YYYY-MM-DD" the create/edit modals send (toDateOnly), not just
// RFC3339, or the modal save 400s.
func TestParseSprintDate(t *testing.T) {
	dateOnly := "2026-07-01"
	if d, err := parseSprintDate(&dateOnly, "start_date"); err != nil || !d.Valid ||
		d.Time.UTC().Format("2006-01-02") != "2026-07-01" {
		t.Errorf("date-only %q must parse, got valid=%v err=%v", dateOnly, d.Valid, err)
	}

	rfc := "2026-07-14T00:00:00Z"
	if d, err := parseSprintDate(&rfc, "end_date"); err != nil || !d.Valid {
		t.Errorf("RFC3339 %q must parse, got err=%v", rfc, err)
	}

	// nil + blank → NULL timestamptz, no error.
	if d, err := parseSprintDate(nil, "x"); err != nil || d.Valid {
		t.Errorf("nil must be NULL, got valid=%v err=%v", d.Valid, err)
	}
	blank := "   "
	if d, err := parseSprintDate(&blank, "x"); err != nil || d.Valid {
		t.Errorf("blank must be NULL, got valid=%v err=%v", d.Valid, err)
	}

	// Garbage → error.
	junk := "not-a-date"
	if _, err := parseSprintDate(&junk, "x"); err == nil {
		t.Error("garbage must error")
	}
}
