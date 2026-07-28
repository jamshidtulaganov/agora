package handler

import "testing"

func TestLooksLikeSprintAcceptsBothScripts(t *testing.T) {
	// These are the real names in the portal, typed by different people over a
	// year. A rule that only matched "Sprint <number>" would drop half of them.
	for _, name := range []string{
		"Sprint 11", "Sprint(12)", "Спринт 12", "Iyun Sprint  (8)",
		"10 спринт (Июль)", "Sprint 7 (май 2026)", "Sprint Top Tasks",
	} {
		if !looksLikeSprint(name) {
			t.Errorf("%q was not recognised as a sprint", name)
		}
	}
}

func TestLooksLikeSprintRejectsOtherGroups(t *testing.T) {
	// The rest of the portal's workgroups must not be mistaken for sprints —
	// "Входящие Баги" collects more tasks than any sprint and would win the
	// activity ranking outright.
	for _, name := range []string{
		"Входящие Баги", "Запрос на новый функционал", "PM Обртаботка", "",
	} {
		if looksLikeSprint(name) {
			t.Errorf("%q was mistaken for a sprint", name)
		}
	}
}
