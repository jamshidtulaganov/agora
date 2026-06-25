package telegram

import (
	"sync"
	"time"
)

// WizardStep is where a user sits in the "/new" guided create flow
// (workspace → project → type → title).
type WizardStep string

const (
	WizardStepWorkspace WizardStep = "workspace"
	WizardStepProject   WizardStep = "project"
	WizardStepType      WizardStep = "type"
	WizardStepTitle     WizardStep = "title"
)

// wizardTTL bounds how long an abandoned create flow stays in memory.
const wizardTTL = 10 * time.Minute

// WizardState is the in-flight "/new" selection for one Telegram user. The Title
// is captured first (the message the user typed); the rest is chosen via inline
// buttons.
type WizardState struct {
	ChatID      string
	Title       string
	Step        WizardStep
	WorkspaceID string
	ProjectID   string // "" = none
	LabelID     string // "" = none
	expiresAt   time.Time
}

// WizardStore holds per-user create-flow state in memory with a TTL, keyed by
// Telegram user id (a private chat's id equals the user id). DB-free and unit
// testable; the handler layer owns workspace/project/label resolution.
type WizardStore struct {
	mu  sync.Mutex
	m   map[string]*WizardState
	now func() time.Time
}

func NewWizardStore() *WizardStore {
	return &WizardStore{m: make(map[string]*WizardState), now: time.Now}
}

// SetClock overrides the clock (tests). Nil restores time.Now.
func (s *WizardStore) SetClock(clock func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if clock == nil {
		clock = time.Now
	}
	s.now = clock
}

// Start begins a fresh wizard at the workspace step with the captured title,
// replacing any prior one.
func (s *WizardStore) Start(tgID, chatID, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[tgID] = &WizardState{
		ChatID:    chatID,
		Title:     title,
		Step:      WizardStepWorkspace,
		expiresAt: s.now().Add(wizardTTL),
	}
}

// Get returns a copy of the active state, or ok=false when none/expired.
func (s *WizardStore) Get(tgID string) (WizardState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[tgID]
	if !ok || s.now().After(st.expiresAt) {
		if ok {
			delete(s.m, tgID)
		}
		return WizardState{}, false
	}
	return *st, true
}

func (s *WizardStore) mutate(tgID string, fn func(*WizardState)) (WizardState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[tgID]
	if !ok || s.now().After(st.expiresAt) {
		if ok {
			delete(s.m, tgID)
		}
		return WizardState{}, false
	}
	fn(st)
	st.expiresAt = s.now().Add(wizardTTL)
	return *st, true
}

// SetWorkspace records the chosen workspace and advances to the project step.
func (s *WizardStore) SetWorkspace(tgID, wsID string) (WizardState, bool) {
	return s.mutate(tgID, func(st *WizardState) {
		st.WorkspaceID = wsID
		st.Step = WizardStepProject
	})
}

// SetProject records the chosen project ("" = none) and advances to the type step.
func (s *WizardStore) SetProject(tgID, projectID string) (WizardState, bool) {
	return s.mutate(tgID, func(st *WizardState) {
		st.ProjectID = projectID
		st.Step = WizardStepType
	})
}

// SetLabel records the chosen label ("" = none) and advances to the title step.
func (s *WizardStore) SetLabel(tgID, labelID string) (WizardState, bool) {
	return s.mutate(tgID, func(st *WizardState) {
		st.LabelID = labelID
		st.Step = WizardStepTitle
	})
}

// AdvanceTo forces the step, used to auto-skip an empty project/type step.
func (s *WizardStore) AdvanceTo(tgID string, step WizardStep) (WizardState, bool) {
	return s.mutate(tgID, func(st *WizardState) { st.Step = step })
}

// Clear removes the wizard (on completion or cancel).
func (s *WizardStore) Clear(tgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, tgID)
}
