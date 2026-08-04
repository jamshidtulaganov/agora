package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// qaResultBlockRe extracts the ```qa-result``` fenced JSON the run_qa recipe
// appends to its verdict comment. Mirrors the frontend parseQAResultBlock so the
// server persists the SAME structured payload the editor used to parse on read.
var qaResultBlockRe = regexp.MustCompile("(?s)```qa-result\\s*\\n(.*?)```")

// qaResultPayload is the structured verdict the agent emits. baseline_ref /
// branch_sha are intentionally NOT part of the block in P1 — evidence is keyed
// (issue, "", "") so each verdict refreshes one latest-row per issue. P2 widens
// the block to carry the tested sha + baseline ref for per-commit history.
//
// commit_sha / started_at (Phase 3 — run identity) are OPTIONAL: commit_sha is
// `git rev-parse HEAD` of the checkout the gate tested (validated to a 7-40
// hex shape, else discarded); started_at is when the gate began (RFC3339). A
// finished_at in the fence is tolerated (unknown JSON keys are ignored) but
// the capture stamps its own now() — the capture moment is authoritative.
type qaResultPayload struct {
	Verdict     string            `json:"verdict"`
	Summary     string            `json:"summary"`
	Commands    []json.RawMessage `json:"commands"`
	Screenshots []json.RawMessage `json:"screenshots"`
	CommitSha   string            `json:"commit_sha"`
	StartedAt   string            `json:"started_at"`
	// Held as RawMessage, not []qaResultPhase, ON PURPOSE: RawMessage accepts
	// ANY valid JSON value here, so an agent that emits a string, an object, or
	// nonsense for `phases` still gets its verdict captured. Decoding it into a
	// typed slice is a separate, failable step — see PhaseTimings.
	Phases json.RawMessage `json:"phases"`
}

// qaResultPhase is one step of the gate recipe with its wall-clock window, so
// the platform can measure WHERE a gate spends its time instead of seeing one
// opaque task duration. The whole fence is persisted verbatim into
// qa_evidence.result_json, so these ride along with no schema change and are
// queryable directly (see docs/qa-phase-timing.md).
//
// The load-bearing entry is `baseline` with Skipped=true: the recipe runs the
// branch first and only re-runs RED commands against the merge-base, so the
// share of gates that skip the baseline entirely — and the time that saves
// versus the ones that can't — is the whole reason this field exists.
//
// Every field is optional and unvalidated on purpose. Phase timings are
// telemetry, never a gate: a missing, malformed, or absent-entirely `phases`
// array must never cost an agent its verdict. Older agents (and any runtime
// pinned to an older template) simply report nothing here.
type qaResultPhase struct {
	Phase      string `json:"phase"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Skipped    bool   `json:"skipped"`
	Note       string `json:"note"`
}

// PhaseTimings decodes the optional phase array. Any problem — absent, wrong
// type, malformed entries — yields nil rather than an error: a verdict must
// never be lost over its telemetry. The verbatim payload still reaches
// qa_evidence.result_json either way, so a malformed array remains inspectable
// in the row even when this returns nothing.
func (p qaResultPayload) PhaseTimings() []qaResultPhase {
	if len(p.Phases) == 0 {
		return nil
	}
	var out []qaResultPhase
	if json.Unmarshal(p.Phases, &out) != nil {
		return nil
	}
	return out
}

// commitShaRe is the accepted commit_sha shape: 7-40 hex chars (a short or
// full git sha). Anything else — prose, a branch name, an injection attempt —
// fails open to "" (unreported), never an error.
var commitShaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// validCommitSha normalizes an agent-reported sha: trimmed + lowercased when
// it matches the 7-40 hex shape, "" otherwise (fail-open).
func validCommitSha(s string) string {
	trimmed := strings.TrimSpace(s)
	if !commitShaRe.MatchString(trimmed) {
		return ""
	}
	return strings.ToLower(trimmed)
}

// parseFenceTime parses an agent-reported RFC3339 timestamp from a fence
// field. Invalid/absent → a NULL timestamptz (fail-open).
func parseFenceTime(s string) pgtype.Timestamptz {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return pgtype.Timestamptz{}
	}
	ts, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: ts, Valid: true}
}

// QADispatchAutoMarker tags an AUTO-fired run_qa dispatch comment (the
// in_review auto-QA path, maybeRunQAOnInReview) — an inert HTML comment next
// to the agent-protocol marker, invisible in rendered markdown. The capture
// reads it off the trigger comment to record qa_evidence.triggered_by="auto"
// vs "agent" (an agent verdict from a human/agent-fired dispatch). Defined
// here (not in handler) because the service layer parses it and handler
// already imports service.
const QADispatchAutoMarker = "<!--qa-dispatch:auto-->"

// qaTriggeredBy classifies who fired the gate whose verdict is being
// captured: "auto" when the trigger comment carries the auto-dispatch marker,
// else "agent". (The human override endpoint writes "human" directly and
// never goes through this.) Fail-open: unresolvable trigger → "agent".
func (s *TaskService) qaTriggeredBy(ctx context.Context, triggerCommentID pgtype.UUID) string {
	if !triggerCommentID.Valid {
		return "agent"
	}
	trigger, err := s.Queries.GetComment(ctx, triggerCommentID)
	if err != nil {
		return "agent"
	}
	if strings.Contains(trigger.Content, QADispatchAutoMarker) {
		return "auto"
	}
	return "agent"
}

// qaDispatchedAt returns when the gate's dispatch comment was created — the
// only piece of HARNESS truth about this run's clock that the capture path can
// see without a new query. The gate cannot have begun before its own dispatch
// existed, which is enough to catch a fabricated start time.
func (s *TaskService) qaDispatchedAt(ctx context.Context, triggerCommentID pgtype.UUID) (time.Time, bool) {
	if !triggerCommentID.Valid {
		return time.Time{}, false
	}
	trigger, err := s.Queries.GetComment(ctx, triggerCommentID)
	if err != nil || !trigger.CreatedAt.Valid {
		return time.Time{}, false
	}
	return trigger.CreatedAt.Time.UTC(), true
}

// phaseTimingCheck is the SERVER's judgement of whether an agent's self-reported
// phase clock can be trusted. Stored next to the fence (see annotatePhaseCheck)
// so a measurement query can exclude junk instead of averaging it in.
//
// Trust levels:
//   - "ok"           — timestamps are inside the real dispatch→capture window and
//     do not look synthesised.
//   - "estimated"    — plausible window, but the agent clearly ROUNDED (every
//     boundary on an exact minute). Directionally useful, not a measurement.
//   - "implausible"  — contradicts harness truth (starts before dispatch, ends
//     after capture, or a negative duration). Do not aggregate.
//   - "absent"       — no timed phases reported at all.
type phaseTimingCheck struct {
	Trust        string `json:"trust"`
	Reason       string `json:"reason,omitempty"`
	DispatchedAt string `json:"dispatched_at,omitempty"`
	CapturedAt   string `json:"captured_at"`
	// Measured is the phase timeline rebuilt from the agent's STREAM
	// (task_message.created_at — a platform clock), present whenever the run
	// emitted `PHASE:` markers. When it is set it SUPERSEDES the agent's
	// self-reported windows entirely, and Trust reads "measured". Aggregate
	// this, not `phases`.
	Measured []qaResultPhase `json:"measured,omitempty"`
}

// checkPhaseTimings reconciles agent-reported phase windows against the only
// two timestamps the server actually knows: when the gate was dispatched and
// when its verdict landed. PURE, so the policy is unit-testable without a DB.
//
// slack absorbs ordinary clock skew between the agent's machine and the server;
// it is deliberately generous because the goal is catching FABRICATION (a start
// time predating the dispatch by minutes, a phase that ends before it begins),
// not policing a few seconds of drift.
func checkPhaseTimings(phases []qaResultPhase, dispatchedAt time.Time, hasDispatch bool, capturedAt time.Time) phaseTimingCheck {
	const slack = 2 * time.Minute

	out := phaseTimingCheck{CapturedAt: capturedAt.UTC().Format(time.RFC3339)}
	if hasDispatch {
		out.DispatchedAt = dispatchedAt.UTC().Format(time.RFC3339)
	}

	type window struct{ start, end time.Time }
	var windows []window
	for _, ph := range phases {
		if ph.Skipped || ph.StartedAt == "" || ph.FinishedAt == "" {
			continue // a skipped phase carries no clock to check — that is correct
		}
		st, errStart := time.Parse(time.RFC3339, ph.StartedAt)
		fi, errEnd := time.Parse(time.RFC3339, ph.FinishedAt)
		if errStart != nil || errEnd != nil {
			out.Trust, out.Reason = "implausible", "phase "+ph.Phase+" has an unparseable timestamp"
			return out
		}
		if fi.Before(st) {
			out.Trust, out.Reason = "implausible", "phase "+ph.Phase+" finishes before it starts"
			return out
		}
		windows = append(windows, window{st.UTC(), fi.UTC()})
	}
	if len(windows) == 0 {
		out.Trust = "absent"
		return out
	}

	for _, w := range windows {
		if hasDispatch && w.start.Before(dispatchedAt.Add(-slack)) {
			out.Trust = "implausible"
			out.Reason = "a phase starts before the gate was dispatched"
			return out
		}
		if w.end.After(capturedAt.Add(slack)) {
			out.Trust = "implausible"
			out.Reason = "a phase ends after the verdict was captured"
			return out
		}
	}

	// Rounding tell: an agent that read a real clock does not land EVERY
	// boundary on an exact minute. Two or more timed phases all reporting :00
	// seconds is a reconstruction after the fact, not a measurement.
	if len(windows) >= 2 {
		rounded := true
		for _, w := range windows {
			if w.start.Second() != 0 || w.end.Second() != 0 {
				rounded = false
				break
			}
		}
		if rounded {
			out.Trust = "estimated"
			out.Reason = "every phase boundary falls on an exact minute — reported to the nearest minute, not measured"
			return out
		}
	}

	out.Trust = "ok"
	return out
}

// qaPhaseMarkerRe matches the in-band phase announcement the run_qa recipe
// emits, e.g. `PHASE: checks` / `PHASE: baseline skipped — every branch command
// passed`. Same convention as the `PROGRESS:` headline and the
// `RUNNING test_case:` markers: the agent supplies the NAME, the platform
// supplies the CLOCK.
//
// Deliberately NOT anchored to line start. The recipe asks for the marker on a
// line by itself, and the first live run showed agents routinely ignoring that
// — `15/15 tests pass. PHASE: baseline skipped — …` was written mid-line, and a
// line-anchored pattern silently dropped it along with the skip signal that is
// the most valuable thing the gate reports. Recognising the marker wherever it
// appears costs nothing; refusing it on a formatting technicality loses data.
var qaPhaseMarkerRe = regexp.MustCompile(`(?i)PHASE:[^\S\n]*(checks|baseline|smoke|cases|materialize)\b[^\S\n]*([^\n]*)`)

// derivePhasesFromStream reconstructs the phase timeline from the agent's
// STREAM rather than from its self-report. Every timestamp here is a
// task_message.created_at — written by the server as the daemon streams the
// agent's output — so it is a real clock reading no matter what the agent later
// claims in its fence.
//
// A phase runs from its own marker until the next one; the last phase ends at
// endedAt (the task's completion, or the capture moment while it is still in
// flight). A marker whose trailing text mentions skipping records the phase as
// skipped and carries no window, matching the fence contract.
//
// PURE, so the reconstruction is unit-testable without a daemon or a DB.
func derivePhasesFromStream(messages []db.TaskMessage, endedAt time.Time) []qaResultPhase {
	type mark struct {
		phase   string
		note    string
		skipped bool
		at      time.Time
	}
	var marks []mark
	seen := map[string]bool{}
	for _, m := range messages {
		if !m.CreatedAt.Valid || !m.Content.Valid {
			continue
		}
		for _, hit := range qaPhaseMarkerRe.FindAllStringSubmatch(m.Content.String, -1) {
			phase := strings.ToLower(hit[1])
			// FIRST announcement wins. A gate runs each phase once, so a repeat
			// is an agent restating itself (a recap, or quoting the contract
			// back) — honouring it would reopen a closed phase and corrupt the
			// window that already closed correctly.
			if seen[phase] {
				continue
			}
			seen[phase] = true
			trailer := strings.TrimSpace(hit[2])
			marks = append(marks, mark{
				phase:   phase,
				note:    trailer,
				skipped: strings.Contains(strings.ToLower(trailer), "skip"),
				at:      m.CreatedAt.Time.UTC(),
			})
		}
	}
	if len(marks) == 0 {
		return nil
	}

	out := make([]qaResultPhase, 0, len(marks))
	for i, mk := range marks {
		ph := qaResultPhase{Phase: mk.phase, Note: mk.note}
		if mk.skipped {
			ph.Skipped = true
			out = append(out, ph)
			continue
		}
		// The window closes at the NEXT marker of any kind — including a skipped
		// one, which still marks the moment this phase stopped.
		end := endedAt.UTC()
		if i+1 < len(marks) {
			end = marks[i+1].at
		}
		if end.Before(mk.at) {
			end = mk.at // never emit a negative window from clock weirdness
		}
		ph.StartedAt = mk.at.Format(time.RFC3339)
		ph.FinishedAt = end.Format(time.RFC3339)
		out = append(out, ph)
	}
	return out
}

// qaGateStreamPhases finds the agent task this verdict came from and rebuilds
// its phase timeline from the streamed messages. Best-effort: any miss (no
// matching task, no markers, a query error) returns nil and the caller falls
// back to the agent's self-report.
//
// The task is matched on trigger_comment_id — the dispatch comment is what
// fired this gate, so it identifies the run unambiguously even when an issue
// has several QA tasks over its lifetime.
func (s *TaskService) qaGateStreamPhases(ctx context.Context, issue db.Issue, triggerCommentID pgtype.UUID, capturedAt time.Time) []qaResultPhase {
	if !triggerCommentID.Valid {
		return nil
	}
	tasks, err := s.Queries.ListTasksByIssue(ctx, issue.ID)
	if err != nil {
		return nil
	}
	for _, task := range tasks {
		if !task.TriggerCommentID.Valid || task.TriggerCommentID != triggerCommentID {
			continue
		}
		messages, msgErr := s.Queries.ListTaskMessages(ctx, task.ID)
		if msgErr != nil {
			return nil
		}
		// The gate is usually still running when it posts its verdict, so the
		// task has no completed_at yet — the capture moment is the true end.
		endedAt := capturedAt
		if task.CompletedAt.Valid && task.CompletedAt.Time.After(capturedAt) {
			endedAt = task.CompletedAt.Time
		}
		return derivePhasesFromStream(messages, endedAt)
	}
	return nil
}

// annotatePhaseCheck attaches the server's verdict to the stored payload under
// `_phase_timing`. The underscore marks it SERVER-OWNED: everything without one
// is the agent's fence exactly as it wrote it. Re-serialising loses the original
// key order, which nothing depends on — the frontend parses this with a zod
// schema that ignores unknown keys, and no reader compares bytes.
//
// Fails open: if the payload will not round-trip, the original raw is stored
// unannotated rather than lost.
func annotatePhaseCheck(raw string, check phaseTimingCheck) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &obj) != nil {
		return raw
	}
	encoded, err := json.Marshal(check)
	if err != nil {
		return raw
	}
	obj["_phase_timing"] = encoded
	merged, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return string(merged)
}

// captureQAEvidence persists a run_qa verdict comment as a durable qa_evidence
// row so the issue's QA section + the QA cockpit read one indexed row instead of
// re-parsing the timeline. Best-effort + detached: a miss (no block, malformed
// JSON, DB error) silently no-ops — a verdict comment never fails because of it.
// Only structured ```qa-result``` verdicts are captured; free-form comments stay
// in the timeline and the QA section prompts a re-run.
// parseQAResultBlock extracts + validates the ```qa-result``` block from a
// comment. Mirrors the frontend parseQAResultBlock: returns the verbatim JSON +
// parsed payload, or ok=false on no block / malformed JSON / invalid verdict.
func parseQAResultBlock(content string) (raw string, p qaResultPayload, ok bool) {
	m := qaResultBlockRe.FindStringSubmatch(content)
	if m == nil {
		return "", qaResultPayload{}, false
	}
	raw = strings.TrimSpace(m[1])
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", qaResultPayload{}, false
	}
	if p.Verdict != "pass" && p.Verdict != "fail" {
		return "", qaResultPayload{}, false
	}
	return raw, p, true
}

// CaptureQAEvidence is exported so the HTTP comment handler can call it too:
// real agents (daemon/CLI) post their verdict via POST /comments, not the
// internal createAgentComment path.
// CaptureQAEvidence persists a qa-result block and, deterministically, attaches
// the qa:pass / qa:fail LABEL the whole gate machinery keys on. Returns the
// verdict ("pass"/"fail"/"") and whether it NEWLY attached the label — the
// handler caller fires the merge-gate / autoroute triggers only on a new attach
// (so an agent that ALSO set the label via CLI does not double-fire them).
//
// Why the server attaches the label: the run_qa agent is instructed to set
// qa:pass/qa:fail itself, but observed live (SD-588 stress test) writing a
// "QA Verdict: PASS" comment + a valid qa-result verdict WITHOUT running the
// label CLI — so the loop stalled (no label → no merge-gate → never done).
// Deriving the label from the captured verdict makes the gate reliable
// regardless of whether the agent remembered the label step.
//
// Label-first ordering (audit finding — "label-attach failure diverges from
// evidence"): the LABEL is attached BEFORE the qa_evidence row is persisted,
// and a label-attach failure aborts the whole capture (no evidence written).
// The old order wrote evidence first and merely warned on a label failure, so
// the qa_evidence row could carry a fresh "pass" while the qa:pass label never
// landed — the chip (reads evidence.verdict, qa-lens.tsx) would show green
// while the merge gate (reads the LABEL, slice_action.go
// enforceQAGateBeforeDone) stayed blocked. Attaching first means the two
// surfaces can never disagree: either both the label and the evidence are
// live, or NEITHER is — a re-run picks it back up (least invasive fix that
// still guarantees agreement, chosen over a cross-table transaction since
// UpsertQAEvidence and AttachLabelToIssue are separate tables with their own
// upsert/idempotency semantics).
// triggerCommentID (Phase 3) is the dispatch comment that fired this gate,
// when known — read to classify triggered_by (auto vs agent). Zero-value =
// unknown → "agent".
func (s *TaskService) CaptureQAEvidence(ctx context.Context, issue db.Issue, content string, triggerCommentID pgtype.UUID) (verdict string, newlyLabeled bool) {
	raw, p, ok := parseQAResultBlock(content)
	if !ok {
		return "", false
	}
	identity := qaEvidenceIdentity{
		CommitSha:   validCommitSha(p.CommitSha),
		TriggeredBy: s.qaTriggeredBy(ctx, triggerCommentID),
		StartedAt:   parseFenceTime(p.StartedAt),
	}

	// Reconcile the agent's self-reported phase clock against harness truth
	// before the row is written, so a measurement query can exclude junk rather
	// than average it in. Never blocks the verdict — an implausible clock costs
	// the timings their trust, nothing else.
	capturedAt := time.Now()
	dispatchedAt, hasDispatch := s.qaDispatchedAt(ctx, triggerCommentID)
	check := checkPhaseTimings(p.PhaseTimings(), dispatchedAt, hasDispatch, capturedAt)

	// Prefer the STREAM. `PHASE:` markers are stamped with task_message
	// .created_at as the daemon streams them, so they are a platform clock
	// reading; the fence's own timestamps are whatever the agent chose to
	// write down afterwards. When the stream has markers, its timeline wins and
	// the self-report is demoted to an unused claim.
	if measured := s.qaGateStreamPhases(ctx, issue, triggerCommentID, capturedAt); len(measured) > 0 {
		check = phaseTimingCheck{
			Trust:        "measured",
			Reason:       "rebuilt from PHASE: markers in the agent's stream (platform clock)",
			DispatchedAt: check.DispatchedAt,
			CapturedAt:   check.CapturedAt,
			Measured:     measured,
		}
	}
	if check.Trust != "absent" {
		raw = annotatePhaseCheck(raw, check)
		if check.Trust != "ok" && check.Trust != "measured" {
			slog.Info("qa phase timings not trusted", "issue_id", util.UUIDToString(issue.ID),
				"trust", check.Trust, "reason", check.Reason)
		}
	}

	v := strings.ToLower(strings.TrimSpace(p.Verdict))

	// Evidence floor (audit requirement): a "pass" that carries ZERO commands
	// asserted nothing was actually verified. downgradeQAVerdictToStale
	// attaches qa:stale + an explanatory comment INSTEAD of applying qa:pass —
	// the qa_evidence row is deliberately NOT written, so an under-evidenced
	// "pass" never sits in the evidence table reading as green.
	if v == "pass" {
		if reason := s.qaEvidenceFloorGap(ctx, issue, p); reason != "" {
			s.downgradeQAVerdictToStale(ctx, issue, reason)
			return "", false
		}
	}

	label, color := "", ""
	switch v {
	case "pass":
		label, color = "qa:pass", "#22c55e"
	case "fail":
		label, color = "qa:fail", "#ef4444"
	}

	// A verdict with no gate label (e.g. "maybe"/"blocked") has nothing for the
	// merge gate to disagree with — persist evidence unconditionally, same as
	// before.
	if label == "" {
		if err := s.upsertQAEvidenceRow(ctx, issue, raw, p, identity); err != nil {
			return "", false
		}
		s.captureDesignVerdictLabel(ctx, issue, raw)
		return v, false
	}

	newlyLabeled = false
	if s.issueHasLabelName(ctx, issue, label) {
		// Agent already set it (e.g. via CLI) → the label handler already fired
		// triggers for it. The label is confirmed present, so evidence can be
		// persisted safely below; just don't report a NEW attach.
	} else {
		labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, label, color)
		if err != nil {
			slog.Warn("capture qa evidence: ensure label failed — evidence NOT recorded (would disagree with the missing label)",
				"error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
			return "", false
		}
		if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
			IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("capture qa evidence: attach label failed — evidence NOT recorded (would disagree with the missing label)",
				"error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
			return "", false
		}
		newlyLabeled = true
	}

	if err := s.upsertQAEvidenceRow(ctx, issue, raw, p, identity); err != nil {
		// The label is already live at this point; leaving it there would swap
		// the divergence direction (label present, no evidence) instead of
		// closing it. If WE just attached it fresh this call, undo that attach
		// so the two surfaces still agree (both absent); if the label was
		// already present from an EARLIER successful capture, leave it — that
		// earlier capture's own evidence row still stands and is unaffected by
		// this call's failure.
		if newlyLabeled {
			s.DetachIssueLabelByName(ctx, issue, label)
		}
		return "", false
	}

	// The design-compare check (sliceActionDesignCompareContext, design_action.go)
	// nests its own advisory verdict at result_json.design.verdict inside this
	// SAME raw block. Mirror it into a design:pass/design:fail label the moment
	// it's captured — independent of the top-level qa verdict above.
	s.captureDesignVerdictLabel(ctx, issue, raw)

	// A verdict REPLACES the previous one — detach the opposite gate label.
	// Without this a fixed-and-re-passed issue carried BOTH labels forever,
	// and every fail-wins surface (cockpit lane, merge gate, sprint rollup)
	// kept reporting it as "need fix" (the audit's sticky-label defect).
	// hadOpposite is read BEFORE the detach: a pass that displaces a qa:fail
	// is a RECOVERY — the one kind of pass worth an inbox notification.
	opposite := "qa:fail"
	if label == "qa:fail" {
		opposite = "qa:pass"
	}
	hadOpposite := s.issueHasLabelName(ctx, issue, opposite)
	s.DetachIssueLabelByName(ctx, issue, opposite)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("qa evidence: auto-attached gate label from verdict", "issue_id", util.UUIDToString(issue.ID), "label", label)

	// Typed inbox notification — the human loop's push channel: a qa:fail
	// (and a recovery pass) must REACH the responsible humans, not wait to be
	// noticed on the /qa queue. Only fires on a NEWLY landed verdict
	// (newlyLabeled), so a re-posted identical verdict never re-notifies.
	if newlyLabeled {
		s.NotifyQAVerdict(ctx, issue, v, hadOpposite, "agent", pgtype.UUID{}, p.Summary)
	}
	return v, newlyLabeled
}

// qaEvidenceIdentity is the Phase 3 run-identity metadata riding on the
// single current evidence row: the sha the gate tested, who fired it
// (agent|human|auto), and when it began. finished_at is stamped by the
// upsert itself (now()).
type qaEvidenceIdentity struct {
	CommitSha   string
	TriggeredBy string
	StartedAt   pgtype.Timestamptz
}

// upsertQAEvidenceRow writes the qa_evidence row and publishes the ready
// event. Split out of CaptureQAEvidence so the label-first ordering above can
// call it from both branches (labeled and label-less verdicts) without
// duplicating the upsert + publish + log.
func (s *TaskService) upsertQAEvidenceRow(ctx context.Context, issue db.Issue, raw string, p qaResultPayload, identity qaEvidenceIdentity) error {
	if _, err := s.Queries.UpsertQAEvidence(ctx, db.UpsertQAEvidenceParams{
		WorkspaceID: issue.WorkspaceID,
		IssueID:     issue.ID,
		BaselineRef: "",
		BranchSha:   "",
		Verdict:     p.Verdict,
		Summary:     p.Summary,
		ResultJson:  []byte(raw),
		Source:      "agent",
		CommitSha:   identity.CommitSha,
		TriggeredBy: identity.TriggeredBy,
		StartedAt:   identity.StartedAt,
	}); err != nil {
		slog.Warn("capture qa evidence: upsert failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return err
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventQAEvidenceReady,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload: map[string]any{
			"issue_id": util.UUIDToString(issue.ID),
			"verdict":  p.Verdict,
		},
	})
	slog.Info("qa evidence captured", "issue_id", util.UUIDToString(issue.ID), "verdict", p.Verdict)
	return nil
}

// qaEvidenceFloorGap returns a human-readable reason when a "pass" verdict's
// evidence does not clear the floor, or "" when it does. Two independent
// checks:
//  1. ZERO commands — the verdict asserted "pass" without a single command
//     result to back it. Always required, regardless of the issue's cases.
//  2. For an issue with at least one UI-modality test case: some VISUAL
//     evidence — a screenshot in the verdict, or a captured Playwright trace
//     on one of the issue's automated runs. Command exit codes alone don't
//     prove a UI case was actually driven and checked.
// Best-effort: a query error on the (2) check is treated as "no gap" (fail
// open on the SECOND, stricter check only — the first, unconditional zero-
// commands check never depends on a query and always applies).
func (s *TaskService) qaEvidenceFloorGap(ctx context.Context, issue db.Issue, p qaResultPayload) string {
	if len(p.Commands) == 0 {
		return "the verdict carried zero commands — nothing ran to prove it"
	}
	if len(p.Screenshots) > 0 {
		return ""
	}
	// The visual-evidence bar (a screenshot or a captured Playwright trace for a
	// UI case) fires ONLY when the change is POSITIVELY high-blast-radius. For
	// everything else — trivial, small, or UNKNOWN scope — the non-zero
	// deterministic commands are the proportionate floor (the zero-commands
	// check above already rejects a truly-hollow pass). This is the
	// deterministic-first stance: a fast DOM-text smoke is the norm, a trace is
	// the exception reserved for guarded / large changes. Requiring a trace by
	// DEFAULT dead-ended every light-QA pass as qa:stale — worst on the common
	// no-PR (sprint-mode) path, where there's no diff signal at all, so an
	// unknown-scope UI change was wrongly forced to the heaviest bar.
	if !s.qaRequiresVisualEvidence(ctx, issue) {
		return ""
	}
	cases, err := s.Queries.ListTestCasesForIssue(ctx, db.ListTestCasesForIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return ""
	}
	hasUICase := false
	for _, c := range cases {
		if c.Modality == "ui" {
			hasUICase = true
			break
		}
	}
	if !hasUICase {
		return ""
	}
	runs, err := s.Queries.ListLatestRunsForIssueCases(ctx, db.ListLatestRunsForIssueCasesParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return ""
	}
	for _, r := range runs {
		if strings.TrimSpace(r.TracePath) != "" {
			return ""
		}
	}
	return "this issue has a UI-modality test case but the verdict carried no screenshot and no run has a captured trace"
}

// qaRequiresVisualEvidence reports whether a UI-case "pass" must carry a
// screenshot or a captured Playwright trace to clear the evidence floor. It
// returns true ONLY for a POSITIVELY high-blast-radius change:
//   - an explicit risk:guarded / risk:critical label, OR
//   - a confirmed LARGE PR diff (more than 2 files or more than 20 changed
//     lines) — where the visual bar is worth the ceremony and the full-scope
//     QA drives the browser anyway (run_qa mandates capturing a trace there).
//
// Everything else returns false — the deterministic commands suffice:
//   - trivial/light/safe/docs labels (light QA sheds the trace ceremony), and
//   - UNKNOWN scope (no PR, e.g. sprint-mode's shared-branch flow) — there is
//     no diff signal, so we must NOT default an unknown change to the heaviest
//     bar; that is exactly what dead-ended light QA as qa:stale. The zero-
//     commands check upstream still rejects a pass that ran nothing.
//
// Mirrors the blast-radius half of handler.issueQAScopeTrivial (the handler
// package can't be imported here — it depends on service, so importing it back
// would cycle); the inversion of the no-PR default is deliberate and specific
// to the evidence floor, not the gate-depth decision.
func (s *TaskService) qaRequiresVisualEvidence(ctx context.Context, issue db.Issue) bool {
	if labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	}); err == nil {
		has := make(map[string]bool, len(labels))
		for _, l := range labels {
			has[strings.ToLower(strings.TrimSpace(l.Name))] = true
		}
		if has["risk:guarded"] || has["risk:critical"] {
			return true // high blast radius — earns the full visual-evidence bar
		}
		if has["tier:trivial"] || has["tier:light"] || has["risk:safe"] || has["type:docs"] {
			return false // light gate — deterministic commands suffice
		}
	}
	prs, err := s.Queries.ListPullRequestsByIssue(ctx, issue.ID)
	if err != nil || len(prs) == 0 {
		return false // no diff signal (unknown / sprint-mode) — don't force a trace
	}
	pr := prs[0]
	if pr.ChangedFiles == 0 {
		// Zero-stat PR row: the PR-open webhook created it but diff stats never
		// synced. UNKNOWN, not "confirmed large" — do not arm the visual bar on
		// a diff we cannot actually see (this exact hole staled honest light
		// passes on every freshly-opened PR).
		return false
	}
	small := pr.ChangedFiles <= 2 && (pr.Additions+pr.Deletions) <= 20
	return !small // only a confirmed LARGE diff earns the visual bar
}

// downgradeQAVerdictToStale attaches qa:stale (replacing qa:pass/qa:fail, if
// either is present) and posts a loud system comment explaining why the
// verdict was not applied, instead of accepting an under-evidenced "pass".
// Mirrors EscalateStaleQAGate's framing (qa:stale = "the gate didn't produce
// a trustworthy result", not a test failure) so this reads consistently with
// the watchdog's own stale escalation. The qa_evidence row is intentionally
// NEVER written here — an insufficiently-evidenced "pass" must not sit in the
// evidence table at all, or the chip would still show a stale-but-green
// summary.
func (s *TaskService) downgradeQAVerdictToStale(ctx context.Context, issue db.Issue, reason string) {
	labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, "qa:stale", "#f59e0b")
	if err != nil {
		slog.Warn("qa evidence floor: ensure qa:stale label failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("qa evidence floor: attach qa:stale failed", "error", err, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	s.DetachIssueLabelByName(ctx, issue, "qa:pass")
	s.DetachIssueLabelByName(ctx, issue, "qa:fail")
	note := "⚠️ QA reported a \"pass\" verdict, but " + reason + " — insufficient evidence — verdict not applied. " +
		"Marking qa:stale (not qa:fail — this is a missing-evidence problem, not a proven test failure); re-run QA with real evidence."
	if _, cerr := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "system",
		AuthorID:    pgtype.UUID{Valid: true},
		Content:     note,
		Type:        "system",
		ParentID:    pgtype.UUID{Valid: false},
	}); cerr != nil {
		slog.Warn("qa evidence floor: system comment failed", "error", cerr, "issue_id", util.UUIDToString(issue.ID))
	}
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Warn("qa evidence floor: pass verdict downgraded to qa:stale — insufficient evidence",
		"issue_id", util.UUIDToString(issue.ID), "reason", reason)
}

// designVerdictFromResult extracts result_json.design.verdict from a raw
// qa-result JSON block. Mirrors designVerdictOf (design_action.go) — a small,
// intentional duplicate rather than a shared package: handler already imports
// service (Handler.TaskService), so service importing handler back would be a
// cycle. The design-compare appendix embeds its verdict in the SAME qa-result
// JSON blob CaptureQAEvidence already parses above, so this reads that raw
// text directly instead of adding a new capture path.
func designVerdictFromResult(raw string) string {
	if raw == "" {
		return ""
	}
	var r struct {
		Design *struct {
			Verdict string `json:"verdict"`
		} `json:"design"`
	}
	if json.Unmarshal([]byte(raw), &r) != nil || r.Design == nil {
		return ""
	}
	return r.Design.Verdict
}

// captureDesignVerdictLabel mirrors the qa:pass/qa:fail attach above for the
// ADVISORY design-compare verdict nested at result_json.design.verdict (see
// sliceActionDesignCompareContext, design_action.go). Same replace-on-write
// semantics: attaching one detaches the other, the label is auto-created per
// workspace if missing. "skipped" (Figma unreachable) and "" (no
// design-compare ran, e.g. the issue has no Figma refs) touch nothing — never
// fail an issue for an infra reason, per the recipe's own doctrine.
func (s *TaskService) captureDesignVerdictLabel(ctx context.Context, issue db.Issue, raw string) {
	v := strings.ToLower(strings.TrimSpace(designVerdictFromResult(raw)))
	label, color := "", ""
	switch v {
	case "pass":
		label, color = "design:pass", "#22c55e"
	case "fail":
		label, color = "design:fail", "#ef4444"
	default:
		return
	}
	if s.issueHasLabelName(ctx, issue, label) {
		return // already set → nothing new to do
	}
	labelID, err := s.ensureLabel(ctx, issue.WorkspaceID, label, color)
	if err != nil {
		slog.Warn("capture design verdict: ensure label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	if err := s.Queries.AttachLabelToIssue(ctx, db.AttachLabelToIssueParams{
		IssueID: issue.ID, LabelID: labelID, WorkspaceID: issue.WorkspaceID,
	}); err != nil {
		slog.Warn("capture design verdict: attach label failed", "error", err, "label", label, "issue_id", util.UUIDToString(issue.ID))
		return
	}
	// A verdict REPLACES the previous one — same "opposite gate label" rule
	// CaptureQAEvidence enforces for qa:pass/qa:fail above.
	opposite := "design:fail"
	if label == "design:fail" {
		opposite = "design:pass"
	}
	s.DetachIssueLabelByName(ctx, issue, opposite)
	s.Bus.Publish(events.Event{
		Type:        protocol.EventIssueLabelsChanged,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     "",
		Payload:     map[string]any{"issue_id": util.UUIDToString(issue.ID)},
	})
	slog.Info("qa evidence: auto-attached design gate label from verdict", "issue_id", util.UUIDToString(issue.ID), "label", label)
}

// DetachIssueLabelByName removes a label (matched case-insensitively by name)
// from an issue. Best-effort — a miss or error is a no-op. Exported so the
// label handler can reuse it for the qa:pass/qa:fail replace semantics.
func (s *TaskService) DetachIssueLabelByName(ctx context.Context, issue db.Issue, name string) {
	labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return
	}
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l.Name), name) {
			if err := s.Queries.DetachLabelFromIssue(ctx, db.DetachLabelFromIssueParams{
				IssueID: issue.ID, LabelID: l.ID, WorkspaceID: issue.WorkspaceID,
			}); err != nil {
				slog.Warn("detach label by name failed", "error", err, "label", name, "issue_id", util.UUIDToString(issue.ID))
			}
			return
		}
	}
}

// issueHasLabelName reports whether the issue already carries a label by name.
func (s *TaskService) issueHasLabelName(ctx context.Context, issue db.Issue, name string) bool {
	labels, err := s.Queries.ListLabelsByIssue(ctx, db.ListLabelsByIssueParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		return false
	}
	for _, l := range labels {
		if strings.EqualFold(strings.TrimSpace(l.Name), name) {
			return true
		}
	}
	return false
}
