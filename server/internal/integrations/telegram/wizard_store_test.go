package telegram

import (
	"testing"
	"time"
)

func TestWizardStore_Flow(t *testing.T) {
	s := NewWizardStore()
	s.Start("u1", "u1", "Fix login")

	st, ok := s.Get("u1")
	if !ok || st.Step != WizardStepWorkspace || st.Title != "Fix login" {
		t.Fatalf("start: %+v ok=%v", st, ok)
	}

	if _, ok := s.SetWorkspace("u1", "ws1"); !ok {
		t.Fatal("SetWorkspace returned false")
	}
	st, _ = s.Get("u1")
	if st.Step != WizardStepProject || st.WorkspaceID != "ws1" {
		t.Fatalf("after workspace: %+v", st)
	}

	s.SetProject("u1", "pj1")
	st, _ = s.Get("u1")
	if st.Step != WizardStepType || st.ProjectID != "pj1" {
		t.Fatalf("after project: %+v", st)
	}

	s.SetLabel("u1", "lb1")
	st, _ = s.Get("u1")
	if st.Step != WizardStepTitle || st.LabelID != "lb1" {
		t.Fatalf("after label: %+v", st)
	}

	s.Clear("u1")
	if _, ok := s.Get("u1"); ok {
		t.Fatal("expected cleared")
	}
}

func TestWizardStore_Expiry(t *testing.T) {
	s := NewWizardStore()
	now := time.Unix(1000, 0)
	s.SetClock(func() time.Time { return now })
	s.Start("u1", "u1", "t")
	now = now.Add(wizardTTL + time.Second)
	if _, ok := s.Get("u1"); ok {
		t.Fatal("expected expired state to be gone")
	}
}

func TestWizardStore_SetOnMissingIsNoop(t *testing.T) {
	s := NewWizardStore()
	if _, ok := s.SetWorkspace("nobody", "ws"); ok {
		t.Fatal("set on a missing wizard should report ok=false")
	}
}
