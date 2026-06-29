autopilot-triggers-readiness.mdx grounded in:

SCHEDULER — server/cmd/server/autopilot_scheduler.go
- schedulerInterval = 30 * time.Second (L14)
- runAutopilotScheduler: calls recoverLostTriggers ONCE before ticker loop (L17-32)
- recoverLostTriggers: RecoverLostTriggers query; skips invalid/empty CronExpression; tz default DefaultAutopilotTriggerTimezone overridden by t.Timezone; ComputeNextRun; AdvanceTriggerNextRun; does NOT dispatch (L36-69)
- tickScheduledAutopilots: ClaimDueScheduleTriggers; per trigger GetAutopilot, DispatchAutopilot(...,"schedule",nil), advanceNextRun (L72-107)
- advanceNextRun: skip invalid/empty cron; tz default UTC override; ComputeNextRun; AdvanceTriggerNextRun (L110-139)

CRON — server/internal/service/cron.go
- cronParser = cron.NewParser(Minute|Hour|Dom|Month|Dow) -> standard 5-field (L11)
- ComputeNextRun: parse cron err, LoadLocation err, sched.Next(now.In(loc)) (L15-25)
- ValidateTimezone (L28-34)
- robfig/cron/v3 import (L7)

WEBHOOK — server/internal/handler/autopilot_webhook.go
- maxWebhookBodyBytes = 256*1024 (L32)
- webhookTokenPrefix = "awt_" (L38); generateWebhookToken = awt_ + base64.RawURLEncoding(32 bytes) = 47 chars (L34-51)
- sigStatus consts: not_required, valid, invalid, missing (L54-59)
- delivery status consts: queued, dispatched, rejected, ignored, failed (L70-76)
- WebhookEnvelope{Event, EventPayload json.RawMessage, Request} (L85-94)
- normalizeWebhookPayload: reject scalars/invalid/empty; object/array only; caller envelope preserved; else inferEvent; default webhook.received (L96-169)
- inferEvent order: X-GitHub-Event+body.action, X-Gitlab-Event, X-Event-Type, body.event, body.type, body.action (L171-199)
- extractDedupeKey: github->X-GitHub-Delivery, generic->Idempotency-Key (L221-232)
- verifyWebhookSignatureForProvider: ""->not_required; no header->missing; bad->invalid; ok->valid (L244-257)
- verifyHubSignature: sha256=<hex>, hmac.Equal constant-time (L262-274)
- selectedHeadersJSON: signature recorded present-only x-hub-signature-256-present=true (L280-301)
- HandleAutopilotWebhook flow comment L305-342 + code: only unauthenticated path, token IS credential (L306-307); steps: IP rate limit before DB (L350-360), token lookup ErrNoRows->404 else 500 (L366-375), per-token rate limit (L380-385), MaxBytesReader 413 (L390-400), GetAutopilot + workspace consistency before persist (L406-426), normalize 400 no-persist (L431-435), provider+dedupe+sig (L443-448), persist queued + dedupe collision->duplicate 200 bump attempt_count (L451-485), sig invalid/missing->rejected 401 (L489-502), disabled/archived/paused->ignored 200 (L508-525), event filter scope->ignored 200 (L530-540), DispatchAutopilot(...,"webhook",envelopeBytes) sync (L545-551), TouchAutopilotTriggerFiredAt after dispatch incl skipped path (L572-579), delivery always dispatched once run returns; skipped/duplicate are response statuses (L581-607)
- webhookEventAllowedByTriggerScope: NULL/empty allow-all; malformed JSON fail-closed return false with slog.Warn (L663-704); no short-circuit on event-name hit per PR#3231 (L696-702)
- validateWebhookEventFilters write-time (L624-636); persistInboundDelivery (L780-826); finaliseDeliveryWithRun / finaliseDeliveryTerminal (L831-885)

AUTOPILOT SERVICE — server/internal/service/autopilot.go
- DefaultAutopilotTriggerTimezone = "UTC" (L40)
- DispatchAutopilot: shouldSkipDispatch first -> recordSkippedRun (L59-68); execution_mode create_issue/run_only; handleDispatchSkip on errDispatchSkipped (L89-113)
- resolveAutopilotLeader: ""/"agent"->GetAgent; "squad"->GetSquad leader, ArchivedAt->errSquadArchived; squadResolved bool (L750-771)
- AgentReadiness shared source-of-truth (agent_ready.go)
- shouldSkipDispatch: MUL-1899; no assignee->skip; hard-skip ErrNoRows/errSquadArchived reasons "assignee squad is archived"/"assignee squad cannot be resolved"/"assignee agent no longer exists"; transient->fail-open ("",false); private-agent visibility gate fails closed (L606-698)
- formatAdmissionReason: squad leader prefix; "at dispatch time" suffix preserved (L700-724)
- errDispatchSkipped sentinel; handleDispatchSkip rewrites running/issue_created -> skipped via UpdateAutopilotRunSkipped, bumps last_run_at, publishRunDone "skipped" (L302-316, L556-595)
- dispatchRunOnly belt-and-braces re-check, errDispatchSkipped on race (L318-391)
- recordSkippedRun: CreateAutopilotRun status skipped + UpdateAutopilotRunSkipped + UpdateAutopilotLastRunAt + publishRunDone skipped (L784-832)

AGENT READINESS — server/internal/service/agent_ready.go
- AgentReadiness: archived->"agent is archived"; no runtime->"agent has no runtime bound"; runtime not online->"agent runtime is <status>"; err only on runtime DB lookup; shared by shouldSkipDispatch/dispatchRunOnly/isSquadLeaderReady (L9-43)

FAILURE MONITOR — server/cmd/server/autopilot_failure_monitor.go
- defaultFailureMonitorConfig: Interval 24h, Lookback 7*24h, MinRuns 50, FailRatio 0.9, StartupDelay 1m (L37-45)
- env overrides AUTOPILOT_FAIL_MONITOR_* (L47-59); Interval<=0 disables
- runAutopilotFailureMonitor: disable on interval<=0; startup delay stagger; run once immediately; ticker (L71-110)
- tickAutopilotFailureMonitor: SelectAutopilotsExceedingFailureThreshold since=now-lookback; SystemPauseAutopilot ErrNoRows benign continue; emitAutopilotPausedNotifications; EventAutopilotUpdated reason auto_paused_high_failure_rate (L114-179)
- emitAutopilotPausedNotifications: inbox type autopilot_paused severity attention (L193-266)
- resolveAutopilotPausedRecipients: member creator -> member; agent creator -> OwnerID->member, else skip silently (L274-306)

SQL — server/pkg/db/queries/autopilot.sql
- ClaimDueScheduleTriggers: UPDATE SET next_run_at=NULL WHERE next_run_at IS NOT NULL AND <= now() AND a.status='active'; claim = nulling column, replica-safe (L230-242)
- RecoverLostTriggers: SELECT next_run_at IS NULL ... enabled, cron not null, active (L274+)
- SelectAutopilotsExceedingFailureThreshold: total = completed+failed, failed; skipped excluded from numerator AND denominator; active + total>=min_runs + failed/total>=fail_ratio (L291-320)
- SystemPauseAutopilot: UPDATE WHERE status='active' RETURNING; no rows = already paused (L322-330)

Audience: both (dev + agent). All inline code copied verbatim from source. No invented APIs. Cross-links only to existing pages: autopilot-internals, squads-routing, agent-runtime-contract, conventions.