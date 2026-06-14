package metrics

import (
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

const (
	labelSource         = "source"
	labelRuntimeMode    = "runtime_mode"
	labelProvider       = "provider"
	labelTerminalStatus = "terminal_status"
	labelFailureReason  = "failure_reason"
	labelTokenType      = "token_type"
	labelModel          = "model"
	labelModelAlias     = "model_alias"

	// PR3 labels (funnel / community / commercial).
	labelSignupSource = "signup_source"
	labelPlatform     = "platform"
	labelPath         = "path"
	labelCadence      = "cadence"
	labelTriggerKind  = "trigger_kind"
	labelReason       = "reason"
	labelRecoverable  = "recoverable"
	labelKind         = "kind"
	labelStatus       = "status"
	labelEventKind    = "event_kind"
	labelAction       = "action"
	labelResult       = "result"
	labelOp           = "op"
)

var businessMetricLabels = map[string][]string{
	"tandem_agent_task_enqueued_total":     {labelSource, labelRuntimeMode},
	"tandem_agent_task_dispatched_total":   {labelSource, labelRuntimeMode},
	"tandem_agent_task_started_total":      {labelSource, labelRuntimeMode, labelProvider},
	"tandem_agent_task_terminal_total":     {labelSource, labelRuntimeMode, labelTerminalStatus},
	"tandem_agent_task_failed_total":       {labelSource, labelRuntimeMode, labelFailureReason},
	"tandem_agent_task_queue_wait_seconds": {labelSource, labelRuntimeMode},
	"tandem_agent_task_run_seconds":        {labelSource, labelRuntimeMode, labelTerminalStatus},
	"tandem_agent_task_total_seconds":      {labelSource, labelRuntimeMode, labelTerminalStatus},
	"tandem_agent_task_in_progress":        {labelSource, labelRuntimeMode},
	"tandem_agent_task_iteration_count":    {labelSource, labelTerminalStatus},
	"tandem_llm_tokens_total":              {labelProvider, labelModel, labelTokenType, labelRuntimeMode, labelSource},
	"tandem_llm_cost_usd_total":            {labelProvider, labelModel, labelTokenType, labelRuntimeMode, labelSource},
	"tandem_llm_unpriced_tokens_total":     {labelProvider, labelModelAlias, labelTokenType},
	"tandem_llm_request_total":             {labelProvider, labelModel, labelRuntimeMode},
	"tandem_task_queued_expired_total":     {labelSource, labelRuntimeMode},
	"tandem_task_lease_expired_total":      {labelSource},

	// PR3 funnel / community / commercial.
	"tandem_signup_total":                             {labelSignupSource},
	"tandem_workspace_created_total":                  {labelSource},
	"tandem_team_invite_sent_total":                   {},
	"tandem_team_invite_accepted_total":               {},
	"tandem_onboarding_started_total":                 {labelPlatform},
	"tandem_onboarding_questionnaire_submitted_total": {},
	"tandem_onboarding_completed_total":               {labelPath},
	"tandem_cloud_waitlist_joined_total":              {},
	"tandem_issue_created_total":                      {labelSource, labelPlatform},
	"tandem_chat_message_sent_total":                  {labelPlatform},
	"tandem_agent_created_total":                      {labelRuntimeMode, labelSource},
	"tandem_squad_created_total":                      {},
	"tandem_autopilot_created_total":                  {labelCadence},
	"tandem_issue_executed_total":                     {labelSource},
	"tandem_runtime_registered_total":                 {labelRuntimeMode, labelProvider},
	"tandem_runtime_ready_total":                      {labelRuntimeMode, labelProvider},
	"tandem_runtime_ready_seconds":                    {labelRuntimeMode, labelProvider},
	"tandem_runtime_failed_total":                     {labelRuntimeMode, labelProvider, labelFailureReason, labelRecoverable},
	"tandem_runtime_offline_total":                    {labelRuntimeMode, labelProvider},
	"tandem_daemon_ws_message_received_total":         {labelKind},
	"tandem_autopilot_run_started_total":              {labelCadence, labelTriggerKind},
	"tandem_autopilot_run_terminal_total":             {labelCadence, labelTriggerKind, labelTerminalStatus},
	"tandem_autopilot_run_skipped_total":              {labelCadence, labelReason},
	"tandem_webhook_delivery_total":                   {labelProvider, labelStatus},
	"tandem_github_event_received_total":              {labelEventKind, labelAction},
	"tandem_github_pr_review_total":                   {labelResult},
	"tandem_cloudruntime_request_total":               {labelOp, labelStatus},
	"tandem_cloudruntime_request_duration_seconds":    {labelOp},
	"tandem_feedback_submitted_total":                 {labelKind, labelPlatform},
	"tandem_contact_sales_submitted_total":            {labelSource},
}

var forbiddenMetricLabels = map[string]struct{}{
	"workspace_id": {},
	"user_id":      {},
	"agent_id":     {},
	"task_id":      {},
	"issue_id":     {},
	"runtime_id":   {},
	"session_id":   {},
	"ip":           {},
}

var (
	knownSources = map[string]string{
		"issue":           "issue",
		"chat":            "chat",
		"autopilot":       "autopilot",
		"autopilot_issue": "autopilot_issue",
		"quick_create":    "quick_create",
		"manual":          "manual",
		"api":             "api",
		"other":           "other",
	}
	knownRuntimeModes = map[string]string{
		"local":   "local",
		"cloud":   "cloud",
		"unknown": "unknown",
	}
	knownRuntimeProviders = map[string]string{
		"antigravity":   "antigravity",
		"claude":        "claude",
		"codebuddy":     "codebuddy",
		"codex":         "codex",
		"copilot":       "copilot",
		"cursor":        "cursor",
		"gemini":        "gemini",
		"hermes":        "hermes",
		"kiro":          "kiro",
		"kimi":          "kimi",
		"tandem_agent": "tandem_agent",
		"openclaw":      "openclaw",
		"opencode":      "opencode",
		"pi":            "pi",
		"other":         "other",
	}
	knownTerminalStatuses = map[string]string{
		"completed": "completed",
		"failed":    "failed",
		"cancelled": "cancelled",
		"blocked":   "blocked",
		"other":     "other",
	}
	knownTokenTypes = map[string]string{
		"input":       "input",
		"output":      "output",
		"cache_read":  "cache_read",
		"cache_write": "cache_write",
	}
	knownFailureReasons = map[string]string{}
	modelAliasUnsafeRe  = regexp.MustCompile(`[^a-z0-9._:/+-]+`)
)

func init() {
	for _, reason := range taskfailure.AllReasons() {
		knownFailureReasons[reason.String()] = reason.String()
	}
}

func validateBusinessMetricLabels() {
	for metric, labels := range businessMetricLabels {
		for _, label := range labels {
			if _, forbidden := forbiddenMetricLabels[label]; forbidden {
				panic("forbidden high-cardinality label " + label + " on " + metric)
			}
		}
	}
}

func metricLabels(metric string) []string {
	labels, ok := businessMetricLabels[metric]
	if !ok {
		panic("missing business metric label definition for " + metric)
	}
	return labels
}

func NormalizeTaskSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownSources[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeRuntimeMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownRuntimeModes[value]; ok {
		return normalized
	}
	return "unknown"
}

func NormalizeRuntimeProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownRuntimeProviders[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeTerminalStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownTerminalStatuses[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeFailureReason(value string) string {
	value = strings.TrimSpace(value)
	if normalized, ok := knownFailureReasons[value]; ok {
		return normalized
	}
	return taskfailure.Classify(value).String()
}

func NormalizeTokenType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownTokenTypes[value]; ok {
		return normalized
	}
	return "input"
}

func NormalizeModelAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	value = modelAliasUnsafeRe.ReplaceAllString(value, "_")
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
