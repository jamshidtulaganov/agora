package service

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

const orchestrationHandoffSchemaVersion = 1

var orchestrationHandoffBlockRe = regexp.MustCompile("(?s)```agora-handoff\\s*\\n(.*?)```")

// OrchestrationHandoff is the stable contract passed between persisted
// orchestration steps. Git state remains independently verified by the server;
// this envelope carries the decisions and evidence that Git cannot express.
type OrchestrationHandoff struct {
	SchemaVersion int                         `json:"schema_version"`
	Stage         string                      `json:"stage"`
	Outcome       string                      `json:"outcome"`
	Verdict       string                      `json:"verdict,omitempty"`
	Summary       string                      `json:"summary"`
	Decisions     []string                    `json:"decisions"`
	Contracts     []string                    `json:"contracts"`
	Artifacts     []OrchestrationArtifact     `json:"artifacts"`
	Verification  []OrchestrationVerification `json:"verification"`
	Findings      []string                    `json:"findings"`
	Risks         []string                    `json:"risks"`
	Blockers      []string                    `json:"blockers"`
	NextActions   []string                    `json:"next_actions"`
	Question      *OrchestrationQuestion      `json:"question,omitempty"`
	Legacy        bool                        `json:"legacy,omitempty"`
}

type OrchestrationArtifact struct {
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	Description string `json:"description,omitempty"`
}

type OrchestrationVerification struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

type OrchestrationQuestion struct {
	Prompt   string `json:"prompt"`
	Target   string `json:"target"`
	TargetID string `json:"target_id,omitempty"`
	Blocking bool   `json:"blocking"`
}

// ParseOrchestrationHandoff extracts the final fenced handoff block from an
// agent result. The server owns the stage value, so an agent cannot make a dev
// result masquerade as QA or release evidence.
func ParseOrchestrationHandoff(stage, output string) (OrchestrationHandoff, bool) {
	matches := orchestrationHandoffBlockRe.FindAllStringSubmatch(output, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		var handoff OrchestrationHandoff
		raw := []byte(strings.TrimSpace(matches[i][1]))
		if normalized, ok := normalizeFlexibleHandoffLists(raw); ok {
			raw = normalized
		}
		if err := json.Unmarshal(raw, &handoff); err != nil {
			continue
		}
		handoff.Stage = stage
		if normalizeOrchestrationHandoff(&handoff) {
			return handoff, true
		}
	}
	return OrchestrationHandoff{}, false
}

// Some providers preserve more structure than the prompt asks for and emit
// objects in the human-readable string arrays (for example
// {"decision":"..."} in decisions). Rejecting the entire gate because one
// list item is richer than expected loses an otherwise valid verdict. Coerce
// those objects to concise strings while leaving the strongly typed artifact,
// verification, and question fields strict.
func normalizeFlexibleHandoffLists(raw []byte) ([]byte, bool) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return raw, false
	}
	for _, key := range []string{"decisions", "contracts", "findings", "risks", "blockers", "next_actions"} {
		value, exists := object[key]
		if !exists {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(value, &items) != nil {
			continue
		}
		stringsOnly := make([]string, 0, len(items))
		for _, item := range items {
			var text string
			if json.Unmarshal(item, &text) == nil {
				stringsOnly = append(stringsOnly, text)
				continue
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(item, &fields) == nil {
				for _, field := range []string{"details", "decision", "contract", "risk", "blocker", "action", "summary", "name"} {
					if candidate, ok := fields[field]; ok && json.Unmarshal(candidate, &text) == nil && strings.TrimSpace(text) != "" {
						break
					}
				}
			}
			if strings.TrimSpace(text) == "" {
				text = string(item)
			}
			stringsOnly = append(stringsOnly, text)
		}
		encoded, err := json.Marshal(stringsOnly)
		if err != nil {
			return raw, false
		}
		object[key] = encoded
	}
	normalized, err := json.Marshal(object)
	return normalized, err == nil
}

// NormalizeOrchestrationHandoff always returns a typed envelope. During the
// rollout, providers that ignore the fenced-block instruction are represented
// as legacy handoffs instead of silently losing their final output.
func NormalizeOrchestrationHandoff(stage, output string) (OrchestrationHandoff, bool) {
	if handoff, ok := ParseOrchestrationHandoff(stage, output); ok {
		return handoff, true
	}
	summary := strings.TrimSpace(orchestrationHandoffBlockRe.ReplaceAllString(output, ""))
	if summary == "" {
		summary = "Stage completed without a textual summary."
	}
	handoff := OrchestrationHandoff{
		SchemaVersion: orchestrationHandoffSchemaVersion,
		Stage:         stage,
		Outcome:       "completed",
		Summary:       truncateHandoffString(summary, 8000),
		Legacy:        true,
	}
	normalizeOrchestrationHandoff(&handoff)
	return handoff, false
}

func normalizeOrchestrationHandoff(handoff *OrchestrationHandoff) bool {
	handoff.SchemaVersion = orchestrationHandoffSchemaVersion
	handoff.Stage = strings.ToLower(strings.TrimSpace(handoff.Stage))
	switch handoff.Stage {
	case "plan", "dev", "qa", "review", "release":
	default:
		return false
	}
	handoff.Outcome = strings.ToLower(strings.TrimSpace(handoff.Outcome))
	if handoff.Outcome == "" {
		handoff.Outcome = "completed"
	}
	switch handoff.Outcome {
	case "completed", "waiting_input", "blocked":
	default:
		return false
	}
	handoff.Verdict = strings.ToLower(strings.TrimSpace(handoff.Verdict))
	switch handoff.Verdict {
	case "", "pass", "fail", "not_applicable":
	default:
		return false
	}
	if handoff.Verdict == "" && handoff.Stage != "qa" && handoff.Stage != "review" {
		handoff.Verdict = "not_applicable"
	}
	handoff.Summary = truncateHandoffString(strings.TrimSpace(handoff.Summary), 8000)
	if handoff.Summary == "" {
		return false
	}
	handoff.Decisions = normalizeHandoffStrings(handoff.Decisions)
	handoff.Contracts = normalizeHandoffStrings(handoff.Contracts)
	handoff.Findings = normalizeHandoffStrings(handoff.Findings)
	handoff.Risks = normalizeHandoffStrings(handoff.Risks)
	handoff.Blockers = normalizeHandoffStrings(handoff.Blockers)
	handoff.NextActions = normalizeHandoffStrings(handoff.NextActions)
	if len(handoff.Artifacts) > 50 {
		handoff.Artifacts = handoff.Artifacts[:50]
	}
	for i := range handoff.Artifacts {
		handoff.Artifacts[i].Kind = truncateHandoffString(strings.TrimSpace(handoff.Artifacts[i].Kind), 100)
		handoff.Artifacts[i].Ref = truncateHandoffString(strings.TrimSpace(handoff.Artifacts[i].Ref), 2000)
		handoff.Artifacts[i].Description = truncateHandoffString(strings.TrimSpace(handoff.Artifacts[i].Description), 2000)
	}
	if handoff.Artifacts == nil {
		handoff.Artifacts = []OrchestrationArtifact{}
	}
	if len(handoff.Verification) > 50 {
		handoff.Verification = handoff.Verification[:50]
	}
	for i := range handoff.Verification {
		check := &handoff.Verification[i]
		check.Name = truncateHandoffString(strings.TrimSpace(check.Name), 500)
		check.Status = strings.ToLower(strings.TrimSpace(check.Status))
		switch check.Status {
		case "passed", "failed", "skipped":
		default:
			check.Status = "skipped"
		}
		check.Details = truncateHandoffString(strings.TrimSpace(check.Details), 4000)
	}
	if handoff.Verification == nil {
		handoff.Verification = []OrchestrationVerification{}
	}
	if handoff.Question != nil {
		handoff.Question.Prompt = truncateHandoffString(strings.TrimSpace(handoff.Question.Prompt), 4000)
		// Questions currently resume through the authenticated human response
		// endpoint. Cross-agent context travels through dependency handoffs; do
		// not persist an agent/controller target that has no automatic consumer.
		handoff.Question.Target = "human"
		handoff.Question.TargetID = ""
	}
	if handoff.Outcome == "waiting_input" {
		if handoff.Question == nil || handoff.Question.Prompt == "" {
			return false
		}
		handoff.Question.Blocking = true
	}
	if handoff.Outcome == "blocked" && len(handoff.Blockers) == 0 {
		return false
	}
	return true
}

func normalizeHandoffStrings(values []string) []string {
	if len(values) > 50 {
		values = values[:50]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = truncateHandoffString(strings.TrimSpace(value), 2000); value != "" {
			result = append(result, value)
		}
	}
	if result == nil {
		return []string{}
	}
	return result
}

func truncateHandoffString(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes]) + "…"
}
