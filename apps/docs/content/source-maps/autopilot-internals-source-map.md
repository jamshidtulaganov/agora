{
  "page": "developers/agentic/autopilot-internals",
  "claims": [
    {
      "claim": "DispatchAutopilot is the single entry point for schedule/webhook/manual; source does not branch dispatch logic, only execution_mode does.",
      "source": "server/internal/service/autopilot.go:59-132 (DispatchAutopilot signature, switch on autopilot.ExecutionMode; source passed through to CreateAutopilotRun and events)"
    },
    {
      "claim": "shouldSkipDispatch runs first; on skip recordSkippedRun writes status=skipped and returns (run, nil).",
      "source": "server/internal/service/autopilot.go:66-68, 788-832"
    },
    {
      "claim": "initial run status: create_issue -> issue_created, run_only -> running.",
      "source": "server/internal/service/autopilot.go:71-74"
    },
    {
      "claim": "autopilotSquadAttribution sets run.squad_id only when assignee_type='squad'.",
      "source": "server/internal/service/autopilot.go:773-782; queries CreateAutopilotRun squad_id comment autopilot.sql:159-169"
    },
    {
      "claim": "DispatchAutopilot can return non-nil run WITH non-nil error on dispatch-failure branches.",
      "source": "server/internal/service/autopilot.go:92-99 (create_issue), 100-108 (run_only), 109-113 (default) — failRun then return &run, error"
    },
    {
      "claim": "Default execution_mode branch fails with 'unknown execution_mode' and is configuration error type.",
      "source": "server/internal/service/autopilot.go:109-113; autopilotErrorType 941-954"
    },
    {
      "claim": "dispatchCreateIssue: resolveAutopilotLeader, projectSquadAllows skip, tx with IncrementIssueCounter/NextTopPosition/CreateIssueWithOrigin (origin_type=autopilot, creator=leader), UpdateAutopilotRunIssueCreated, publish EventIssueCreated, EnqueueTaskForSquadLeader vs EnqueueTaskForIssue.",
      "source": "server/internal/service/autopilot.go:179-300"
    },
    {
      "claim": "dispatchRunOnly: resolveAutopilotLeader (race -> errDispatchSkipped), AgentReadiness, private-leader gate, CreateAutopilotTask (issue_id NULL, queued, autopilot_run_id, trigger_summary), UpdateAutopilotRunRunning.",
      "source": "server/internal/service/autopilot.go:326-391; CreateAutopilotTask SQL autopilot.sql:248-251"
    },
    {
      "claim": "run_only bypasses TaskService.Enqueue* and MUST call NotifyTaskEnqueued or the daemon never wakes.",
      "source": "server/internal/service/autopilot.go:378-383 and inline comment"
    },
    {
      "claim": "SyncRunFromTask links via task.AutopilotRunID; completed carries task.Result; failed/cancelled fails run.",
      "source": "server/internal/service/autopilot.go:437-481"
    },
    {
      "claim": "SyncRunFromLinkedIssueTask links via issue_id, only on task.Status=='failed' with no active task, waits via HasActiveTaskForIssue, uses taskFailureReasonForAutopilotRun.",
      "source": "server/internal/service/autopilot.go:483-554"
    },
    {
      "claim": "SyncRunFromIssue keys on OriginType='autopilot'; done/in_review -> completed, cancelled/blocked -> failed.",
      "source": "server/internal/service/autopilot.go:393-434"
    },
    {
      "claim": "GetAutopilotRunByIssue matches only status IN ('issue_created','running') LIMIT 1.",
      "source": "server/pkg/db/queries/autopilot.sql:257-260"
    },
    {
      "claim": "Run status state machine queries and fields.",
      "source": "server/pkg/db/queries/autopilot.sql:181-224"
    },
    {
      "claim": "FailAutopilotRunsByIssue must run before deletion due to ON DELETE SET NULL.",
      "source": "server/pkg/db/queries/autopilot.sql:262-268"
    },
    {
      "claim": "Failure-rate monitor counts only completed|failed; skipped and issue_created/running excluded.",
      "source": "server/pkg/db/queries/autopilot.sql:291-320"
    },
    {
      "claim": "handleDispatchSkip rewrites in-flight run to skipped on post-admission errDispatchSkipped race.",
      "source": "server/internal/service/autopilot.go:556-595"
    },
    {
      "claim": "source values schedule/webhook/manual; webhook payload inlined into issue description.",
      "source": "server/internal/service/autopilot.go:1053-1093; SKILL.md:26"
    },
    {
      "claim": "issue_title_template supports only {{date}}; ValidateIssueTitleTemplate rejects others; interpolateTemplate substitutes date in trigger timezone, fallback UTC.",
      "source": "server/internal/service/autopilot.go:1103-1165 (interpolate, Supported..., Validate), 985-1012 (timezone), 40 (DefaultAutopilotTriggerTimezone); SKILL.md:34"
    }
  ],
  "cross_links": [
    "/developers/agentic/autopilot-triggers-readiness (trigger sources, readiness/admission)"
  ]
}