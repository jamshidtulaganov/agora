package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/jamshidtulaganov/agora/server/internal/assistant"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/linear"
	"github.com/jamshidtulaganov/agora/server/internal/util"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// THE MIGRATION CONCIERGE — the assistant's five import tools
// (docs/importers-plan.md §4.3).
//
// A tracker migration is the one job where a conversation beats a wizard: the
// answer is never "240 issues, proceed?" but "240 issues, 12 of which this
// token cannot see, two statuses nobody has mapped, and Dana Wu is not in this
// workspace" — an argument, held in sentences, before anything is written. So
// the flow is: connect in Settings, survey with dry_run_import, argue with the
// plan, and authorize the whole job with ONE click.
//
// Three rules are structural here rather than advisory:
//
//  1. THE TOKEN NEVER REACHES THE ASSISTANT. Every tool below takes a
//     connection_id and nothing else; the plaintext is opened inside an
//     executor, handed straight to the adapter's HTTP client, and dropped. No
//     field of any result carries it, no error message quotes it, and
//     TestAssistantImportToolsNeverLeakTheToken asserts that on the MARSHALED
//     JSON of every tool, because the struct is not what reaches the model.
//     This is assistant.ExcludedCapabilities' one standing "no" applied to
//     an import key: the transcript is persisted, so a secret typed into it
//     outlives the conversation (§3.8).
//
//  2. THE CONFIRMATION BINDS THE JOB, NOT THE ROWS. propose_plan is capped at
//     assistant.MaxPlanItems and a 240-issue import is not 240 plan rows — it
//     is ONE job whose rows live in import_job.plan (§4.2). So confirm_import
//     joins DestructiveTools and parks a pending operation like every other
//     bound tool: the summary names the plan's own counts, the human clicks
//     Confirm once, and only then does the job start.
//
//  3. NOTHING IS WRITTEN BEFORE THAT CLICK. dry_run_import opens a job row in
//     `dry_run`, runs fetch → normalize → map → diff with zero writes, and
//     parks the plan as `awaiting_confirm`. The mapping is frozen alongside it,
//     so a settings edit between survey and confirm cannot change what the
//     human authorized.
//
// What this file deliberately does NOT do is call the HTTP import handlers.
// It drives internal/imports and the generated queries directly, for the same
// reason the rest of the assistant calls handlers where they exist: there is
// no handler to call that adds anything here — the role gate, the membership
// gate and the workspace scoping are all applied below — and a tool that ran
// through a route would have to invent a request the user never made.

// assistantImportSource is the only source Phase 1 ships. It is a variable in
// the tool schema rather than a hard-coded call so a second adapter is a
// registry entry, but until Jira lands an unknown source is refused by name
// instead of failing deep inside a fetch.
const assistantImportSource = imports.SourceLinear

// assistantWhereImport is the place in the app that owns import credentials.
// Every refusal that is really "no connection yet" names it, because a refusal
// without a destination is the failure mode writeIntegrationGuidance exists to
// prevent.
const assistantWhereImport = "Settings → Integrations → Import"

// assistantImportSecretsNote rides with every import answer, for the same
// reason assistantIntegrationSecretsNote does: the system prompt is far away
// by the time a connection list comes back mid-conversation, and this is the
// moment the model is most likely to offer to "just paste the key here".
const assistantImportSecretsNote = "connection ids only — these tools never accept or return an API key. " +
	"A source key is pasted into " + assistantWhereImport + ", never into this conversation; " +
	"if one is pasted here it must be revoked and regenerated at the source."

// assistantImportRunTimeout bounds a confirmed import that runs detached from
// the confirming request. A job that outlives this is finished as failed with
// a reason rather than left in `running` forever, blocking the workspace.
const assistantImportRunTimeout = 30 * time.Minute

// ---------------------------------------------------------------------------
// list_import_connections
// ---------------------------------------------------------------------------

// assistantImportConnectionRow is one connection as the model sees it: what it
// points at, whether the last probe liked it, and nothing else. The listing
// query does not even SELECT secret_encrypted, so this shape cannot leak one
// by accident.
type assistantImportConnectionRow struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Label       string `json:"label,omitempty"`
	ProbeStatus string `json:"probe_status,omitempty"`
	ProbedAt    string `json:"probed_at,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func (h *Handler) assistantListImportConnections(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}

	rows, err := h.Queries.ListImportConnections(ctx, ws.ID)
	if err != nil {
		slog.Warn("assistant: list import connections failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not read the import connections for this workspace")
	}

	out := make([]assistantImportConnectionRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, assistantImportConnectionRow{
			ID:          uuidToString(row.ID),
			Source:      row.Source,
			Label:       row.Label,
			ProbeStatus: row.ProbeStatus,
			ProbedAt:    assistantImportStamp(row.ProbedAt),
			CreatedAt:   assistantImportStamp(row.CreatedAt),
		})
	}

	payload := map[string]any{
		"workspace_id":   uuidToString(ws.ID),
		"workspace_slug": ws.Slug,
		"your_role":      role,
		"connections":    out,
		// Importing is owner/admin work. A plain member asking to migrate
		// should be told who can, rather than walked to a form the server
		// refuses.
		"can_manage":    roleAllowed(role, "owner", "admin"),
		"sources":       []string{assistantImportSource},
		"where":         assistantWhereImport,
		"probe_values":  []string{imports.ProbeOK, imports.ProbeInvalid, imports.ProbeUnreachable},
		"probe_note":    "ok = the key worked when it was last checked; invalid = the source rejected it; unreachable = the source could not be reached. An empty probe_status means it has not been checked since it was saved.",
		"secrets_note":  assistantImportSecretsNote,
		"seal_key_note": "",
	}
	// Fail closed, and SAY SO. Without AGORA_IMPORT_SECRET_KEY no connection
	// can be stored at all, and "there are no connections" would send the user
	// to a page whose save button answers 503 (§3.8).
	if _, sealErr := imports.SecretBox(); sealErr != nil {
		payload["seal_key_note"] = "imports are not enabled on this Agora instance — the instance operator has to set the import seal key in Settings → Configs before any source can be connected"
	}
	if active, ok := h.assistantImportActiveJob(ctx, ws.ID); ok {
		payload["active_job"] = active
	}

	return assistantScopedResult(payload, assistantExactScope(ws.Slug, len(out)))
}

// assistantImportActiveJob reports the workspace's live job, if any. It rides
// on the connection listing and on every refusal to start a second import,
// because "a second import is refused with a POINTER AT the running one"
// (§3.7) is only useful if the pointer travels.
func (h *Handler) assistantImportActiveJob(ctx context.Context, wsID pgtype.UUID) (map[string]any, bool) {
	job, err := h.Queries.GetActiveImportJob(ctx, wsID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("assistant: read active import job failed", "workspace_id", uuidToString(wsID), "error", err)
		}
		return nil, false
	}
	return map[string]any{
		"job_id":     uuidToString(job.ID),
		"source":     job.Source,
		"status":     job.Status,
		"started_at": assistantImportStamp(job.StartedAt),
		"created_at": assistantImportStamp(job.CreatedAt),
	}, true
}

// ---------------------------------------------------------------------------
// dry_run_import
// ---------------------------------------------------------------------------

// assistantImportScopeArgs is the operator's choice of what to import, in the
// model's vocabulary. It maps one-for-one onto imports.Scope; nothing here is
// a credential and nothing here is free-form JSON.
type assistantImportScopeArgs struct {
	Containers         []string `json:"containers"`
	Since              string   `json:"since"`
	IncludeArchived    *bool    `json:"include_archived"`
	IncludeComments    *bool    `json:"include_comments"`
	IncludeAttachments *bool    `json:"include_attachments"`
	ProvisionUsers     *bool    `json:"provision_users"`
	MaxIssues          *int     `json:"max_issues"`
}

type assistantDryRunImportArgs struct {
	WorkspaceID  string                    `json:"workspace_id"`
	ConnectionID string                    `json:"connection_id"`
	Scope        *assistantImportScopeArgs `json:"scope"`
}

func (h *Handler) assistantDryRunImport(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	// Membership FIRST, before any argument validation: a non-member must get
	// the membership refusal rather than a complaint about a missing field,
	// which would leak the ordering of the checks (see
	// TestWorkspaceToolDenialsUseTheMembershipWording).
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	if !roleAllowed(role, "owner", "admin") {
		return nil, errors.New("only an owner or an admin can run an import preview in this workspace")
	}
	var args assistantDryRunImportArgs
	if uerr := json.Unmarshal(raw, &args); uerr != nil {
		return nil, errAssistantBadArgs
	}

	conn, err := h.assistantImportConnection(ctx, ws, args.ConnectionID)
	if err != nil {
		return nil, err
	}
	scope, err := assistantImportScopeOf(args.Scope)
	if err != nil {
		return nil, err
	}

	adapter, err := h.assistantImportAdapter(conn)
	if err != nil {
		return nil, err
	}

	runner := h.assistantImportRunner()
	job, err := runner.Open(ctx, imports.OpenRequest{
		WorkspaceID:  ws.ID,
		ConnectionID: conn.ID,
		Source:       conn.Source,
		Scope:        scope,
		CreatedBy:    caller.UUID,
		Status:       imports.JobDryRun,
	})
	if err != nil {
		return h.assistantImportInFlight(ctx, ws, err)
	}

	mapping := imports.NewMapping(conn.Source, adapter.Defaults(), imports.ParseOverrides(ws.Settings, conn.Source))
	resolver := imports.NewActorResolver(h.assistantImportIdentity(), imports.ResolverConfig{
		Source:      conn.Source,
		WorkspaceID: uuidToString(ws.ID),
		Aliases:     imports.ParseAliases(ws.Settings),
		Provision:   scope.ProvisionUsers,
		// The survey writes NOTHING — not an issue, not a user, not an
		// identity link. Step 4 reports `would_provision` instead of doing it.
		DryRun: true,
	})

	plan, parked, err := runner.Preview(ctx, job, adapter, mapping, resolver)
	if err != nil {
		slog.Warn("assistant: import dry run failed",
			"workspace_id", uuidToString(ws.ID), "job_id", uuidToString(job.ID), "error", err)
		// The adapter's own error is the useful one ("your key was rejected"),
		// and it is credential-free by the Adapter contract.
		return nil, fmt.Errorf("the import preview could not be completed: %w", err)
	}

	payload := assistantImportPlanPayload(plan)
	payload["job_id"] = uuidToString(parked.ID)
	payload["job_status"] = parked.Status
	payload["connection_id"] = uuidToString(conn.ID)
	payload["workspace_id"] = uuidToString(ws.ID)
	payload["workspace_slug"] = ws.Slug
	payload["wrote_nothing"] = true
	payload["next_step"] = "nothing has been written. Report the plan — the unreachable set, the unmatched people and " +
		"the skipped files FIRST — then let the user correct the mapping with update_import_mapping, or ask for " +
		"confirm_import when they are satisfied. Never call confirm_import in the same turn as this preview."
	payload["secrets_note"] = assistantImportSecretsNote
	return json.Marshal(payload)
}

// ---------------------------------------------------------------------------
// update_import_mapping
// ---------------------------------------------------------------------------

// assistantUpdateImportMappingArgs is the "argue with the plan" tool. Every
// map here is small and operator-authored: source status name → Agora status,
// source priority name → Agora priority, source container → existing project,
// and the one identity override the Bitrix importer already proved is needed
// ("that address is this person").
type assistantUpdateImportMappingArgs struct {
	WorkspaceID string            `json:"workspace_id"`
	Source      string            `json:"source"`
	Status      map[string]string `json:"status"`
	Priority    map[string]string `json:"priority"`
	Containers  map[string]string `json:"containers"`
	Users       map[string]string `json:"users"`
}

func (h *Handler) assistantUpdateImportMapping(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	if !roleAllowed(role, "owner", "admin") {
		return nil, errors.New("only an owner or an admin can change the import mapping for this workspace")
	}
	var args assistantUpdateImportMappingArgs
	if uerr := json.Unmarshal(raw, &args); uerr != nil {
		return nil, errAssistantBadArgs
	}
	source := strings.ToLower(strings.TrimSpace(args.Source))
	if source == "" {
		source = assistantImportSource
	}
	if source != assistantImportSource {
		return nil, fmt.Errorf("%q is not a source this Agora version can import from (today: %s)", source, assistantImportSource)
	}
	if len(args.Status) == 0 && len(args.Priority) == 0 && len(args.Containers) == 0 && len(args.Users) == 0 {
		return nil, errors.New("nothing to change — pass at least one of status, priority, containers or users")
	}

	// Read the current document, merge onto it, write the ONE key back. The
	// settings blob is shared with every other feature, so a whole-blob
	// replace here would clobber a sibling key written a second ago.
	existing := imports.ParseOverrides(ws.Settings, source)
	merged := imports.Overrides{
		Status:     assistantImportCopyMap(existing.Status),
		Priority:   assistantImportCopyMap(existing.Priority),
		Containers: assistantImportCopyMap(existing.Containers),
	}
	rejected := []string{}

	for name, status := range args.Status {
		key := strings.ToLower(strings.TrimSpace(name))
		value := strings.ToLower(strings.TrimSpace(status))
		if key == "" {
			continue
		}
		// A bad value is DROPPED AND REPORTED rather than stored: an invalid
		// status in settings would become a CHECK violation mid-run, hours
		// after the human authorized the job.
		if !imports.ValidStatus(value) {
			rejected = append(rejected, fmt.Sprintf("status %q → %q is not an Agora status", name, status))
			continue
		}
		if merged.Status == nil {
			merged.Status = map[string]string{}
		}
		merged.Status[key] = value
	}
	for name, priority := range args.Priority {
		key := strings.ToLower(strings.TrimSpace(name))
		value := strings.ToLower(strings.TrimSpace(priority))
		if key == "" {
			continue
		}
		if !assistantImportValidPriority(value) {
			rejected = append(rejected, fmt.Sprintf("priority %q → %q is not an Agora priority", name, priority))
			continue
		}
		if merged.Priority == nil {
			merged.Priority = map[string]string{}
		}
		merged.Priority[key] = value
	}
	for container, projectRef := range args.Containers {
		key := strings.ToLower(strings.TrimSpace(container))
		if key == "" {
			continue
		}
		// Grounded, not guessed: the value has to be a project that exists in
		// THIS workspace, resolved the same way every other tool resolves one.
		project, perr := h.assistantResolveProject(ctx, ws, projectRef)
		if perr != nil {
			rejected = append(rejected, fmt.Sprintf("container %q → %q: %s", container, projectRef, perr.Error()))
			continue
		}
		if merged.Containers == nil {
			merged.Containers = map[string]string{}
		}
		merged.Containers[key] = uuidToString(project.ID)
	}

	if err := h.assistantImportWriteSettingKey(ctx, ws, imports.SettingsKeyMapping, source, merged); err != nil {
		return nil, err
	}

	aliases := imports.ParseAliases(ws.Settings)
	if len(args.Users) > 0 {
		if aliases == nil {
			aliases = map[string]string{}
		}
		for from, to := range args.Users {
			fromKey := strings.ToLower(strings.TrimSpace(from))
			toKey := strings.ToLower(strings.TrimSpace(to))
			if fromKey == "" || toKey == "" {
				rejected = append(rejected, fmt.Sprintf("user %q → %q needs an address on both sides", from, to))
				continue
			}
			aliases[fromKey] = toKey
		}
		if err := h.assistantImportWriteAliases(ctx, ws, aliases); err != nil {
			return nil, err
		}
	}

	payload := map[string]any{
		"workspace_id":   uuidToString(ws.ID),
		"workspace_slug": ws.Slug,
		"source":         source,
		"mapping": map[string]any{
			"status":     merged.Status,
			"priority":   merged.Priority,
			"containers": merged.Containers,
		},
		"identity_aliases": aliases,
		"rejected":         rejected,
		"note": "the mapping is stored on the workspace and is frozen into the job at confirm time. " +
			"It only affects a run started AFTER this change — re-run dry_run_import to see it applied.",
	}
	return json.Marshal(payload)
}

// assistantImportValidPriority is the issue table's priority vocabulary. It is
// spelled out here rather than imported because imports.Mapping keeps its own
// validator unexported, and one list of five strings is cheaper than a seam.
func assistantImportValidPriority(s string) bool {
	switch s {
	case imports.PriorityNameNone, imports.PriorityNameUrgent, imports.PriorityNameHigh,
		imports.PriorityNameMedium, imports.PriorityNameLow:
		return true
	}
	return false
}

func assistantImportCopyMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// assistantImportWriteSettingKey merges one source's overrides into
// workspace.settings.import_mapping without touching another source's.
func (h *Handler) assistantImportWriteSettingKey(ctx context.Context, ws db.Workspace, key, source string, value imports.Overrides) error {
	doc := map[string]json.RawMessage{}
	if len(ws.Settings) > 0 {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(ws.Settings, &settings); err == nil {
			if blob, ok := settings[key]; ok {
				_ = json.Unmarshal(blob, &doc)
			}
		}
	}
	blob, err := json.Marshal(value)
	if err != nil {
		return errors.New("could not encode the import mapping")
	}
	doc[source] = blob
	merged, err := json.Marshal(doc)
	if err != nil {
		return errors.New("could not encode the import mapping")
	}
	if _, err := h.Queries.SetWorkspaceSettingKey(ctx, db.SetWorkspaceSettingKeyParams{
		ID:    ws.ID,
		Key:   key,
		Value: merged,
	}); err != nil {
		slog.Warn("assistant: write import mapping failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return errors.New("could not save the import mapping")
	}
	return nil
}

func (h *Handler) assistantImportWriteAliases(ctx context.Context, ws db.Workspace, aliases map[string]string) error {
	blob, err := json.Marshal(aliases)
	if err != nil {
		return errors.New("could not encode the identity overrides")
	}
	if _, err := h.Queries.SetWorkspaceSettingKey(ctx, db.SetWorkspaceSettingKeyParams{
		ID:    ws.ID,
		Key:   imports.SettingsKeyAliases,
		Value: blob,
	}); err != nil {
		slog.Warn("assistant: write import aliases failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return errors.New("could not save the identity overrides")
	}
	return nil
}

// ---------------------------------------------------------------------------
// confirm_import
// ---------------------------------------------------------------------------

type assistantConfirmImportArgs struct {
	WorkspaceID string `json:"workspace_id"`
	JobID       string `json:"job_id"`
}

// assistantConfirmImport is the one write in this file, and it is
// confirmation-bound (assistant.DestructiveTools).
//
// It binds to the JOB, not to the rows: the plan the human read is already
// frozen on import_job.plan, so the pending operation's target is that job id
// and its summary is that plan's own counts. A dry run that happens after the
// card was raised produces a DIFFERENT job, so the stale confirmation no
// longer matches its target and is refused rather than silently authorizing a
// survey nobody read (assistantAwaitConfirmation's target check).
func (h *Handler) assistantConfirmImport(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, role, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	if !roleAllowed(role, "owner", "admin") {
		return nil, errors.New("only an owner or an admin can start an import in this workspace")
	}
	var args assistantConfirmImportArgs
	if uerr := json.Unmarshal(raw, &args); uerr != nil {
		return nil, errAssistantBadArgs
	}

	job, err := h.assistantImportJob(ctx, ws, args.JobID)
	if err != nil {
		return nil, err
	}
	plan, err := imports.DecodePlan(job.Plan)
	if err != nil || plan == nil {
		return nil, errors.New("that import has no plan yet — run dry_run_import first and read the plan back to the user before confirming")
	}
	if job.Status != imports.JobAwaitingConfirm {
		return nil, fmt.Errorf("import job %s is %s, so there is nothing to confirm — run dry_run_import for a fresh plan",
			uuidToString(job.ID), job.Status)
	}

	jobID := uuidToString(job.ID)
	out, err := h.assistantAwaitConfirmation(ctx, caller, raw, assistantOperationPlan{
		Tool:      assistant.ToolConfirmImport,
		Summary:   assistantImportSummary(plan, ws),
		Workspace: ws,
		Target: assistantOperationTarget{
			Type:       "import",
			Identifier: jobID,
			Title:      assistantImportSourceLabel(job.Source) + " → " + ws.Name,
		},
	})
	if out != nil || err != nil {
		return out, err
	}

	// Past this line a human has pressed Confirm on a card naming these
	// counts. Everything below is the job actually starting.
	conn, err := h.assistantImportConnectionByUUID(ctx, ws, job.ConnectionID)
	if err != nil {
		return nil, err
	}
	adapter, err := h.assistantImportAdapter(conn)
	if err != nil {
		return nil, err
	}
	scope, err := imports.DecodeScope(job.Scope)
	if err != nil {
		return nil, errors.New("that import's scope could not be read back")
	}

	// The mapping is the one FROZEN with the plan, not whatever settings say
	// now: the human authorized a specific translation table (§4.2).
	frozen := imports.Overrides{}
	if len(job.Mapping) > 0 {
		if uerr := json.Unmarshal(job.Mapping, &frozen); uerr != nil {
			frozen = imports.ParseOverrides(ws.Settings, job.Source)
		}
	}
	mapping := imports.NewMapping(job.Source, adapter.Defaults(), frozen)
	resolver := imports.NewActorResolver(h.assistantImportIdentity(), imports.ResolverConfig{
		Source:      job.Source,
		WorkspaceID: uuidToString(ws.ID),
		Aliases:     imports.ParseAliases(ws.Settings),
		Provision:   scope.ProvisionUsers,
	})
	applier := &imports.Applier{
		Store:       h.Queries,
		Resolver:    resolver,
		Mapping:     mapping,
		WorkspaceID: ws.ID,
		ImportID:    jobID,
		Source:      job.Source,
		Limits:      imports.DefaultLimits(),
		// No attachment sink is wired into the assistant path: copying files
		// needs the storage layer, and the framework's contract is that
		// missing halves are SKIPPED AND LISTED rather than dropped quietly.
		// The receipt says so per issue, and the plan already warned that a
		// file Agora does not copy stays only in the source.
	}

	// Detached, because an import is not in-band: the confirm request returns
	// {job_id, status:"running"} and the model's job is to say what started
	// and stop talking (§4.2). Cancelling the HTTP request must not abandon a
	// claimed job row in `running` forever.
	runner := h.assistantImportRunner()
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), assistantImportRunTimeout)
	go func() {
		defer cancel()
		if _, _, rerr := runner.Run(runCtx, job, adapter, applier); rerr != nil {
			slog.Warn("assistant: import run failed", "workspace_id", uuidToString(ws.ID), "job_id", jobID, "error", rerr)
		}
	}()

	return json.Marshal(map[string]any{
		"job_id":         jobID,
		"status":         imports.JobRunning,
		"source":         job.Source,
		"workspace_id":   uuidToString(ws.ID),
		"workspace_slug": ws.Slug,
		"started":        true,
		"plan_headline":  assistantImportSummary(plan, ws),
		"next_step": "the import is running on the server. Say what was started and where the receipt will appear; " +
			"do NOT poll import_status in a loop and do not invent progress.",
		"secrets_note": assistantImportSecretsNote,
	})
}

// assistantImportSummary is the sentence on the confirmation card. It is built
// from the frozen plan, so the numbers the human authorizes are the numbers
// they were shown — and it leads with the bad news when there is any, because
// a card that reads "Import 240 issues" over a plan with 12 unreachable rows
// is the fastest way to lose a team in week two (§4.4).
func assistantImportSummary(plan *imports.Plan, ws db.Workspace) string {
	if plan == nil {
		return "Import into " + ws.Name
	}
	parts := []string{}
	if plan.Issues.Total > 0 {
		parts = append(parts, assistantCountLabel(plan.Issues.Total, "issue"))
	}
	if len(plan.Containers) > 0 {
		parts = append(parts, assistantCountLabel(len(plan.Containers), "project"))
	}
	if len(plan.Users) > 0 {
		parts = append(parts, assistantCountLabel(len(plan.Users), "member"))
	}
	if plan.Comments.Total > 0 {
		parts = append(parts, assistantCountLabel(plan.Comments.Total, "comment"))
	}
	head := "Import "
	if len(parts) == 0 {
		head = "Import everything found "
	} else {
		head += strings.Join(parts, ", ") + " "
	}
	head += "from " + assistantImportSourceLabel(plan.Source.Kind) + " into " + ws.Name
	if plan.Issues.Update > 0 {
		head += fmt.Sprintf(" (%d already linked — they will be updated, not duplicated)", plan.Issues.Update)
	}
	caveats := []string{}
	if !plan.Exact {
		caveats = append(caveats, "these counts are estimates")
	}
	if n := assistantImportTruncatedTotal(plan); n > 0 {
		caveats = append(caveats, fmt.Sprintf("%d row(s) this key cannot reach are NOT included", n))
	}
	if plan.UnmatchedUsers > 0 {
		caveats = append(caveats, fmt.Sprintf("%d unmatched author(s) will be attributed to %s",
			plan.UnmatchedUsers, imports.ImportIdentityName(plan.Source.Kind)))
	}
	if plan.Attachments.Skipped > 0 {
		caveats = append(caveats, fmt.Sprintf("%d file(s) over budget will be linked, not copied", plan.Attachments.Skipped))
	}
	if len(caveats) > 0 {
		head += " — " + strings.Join(caveats, "; ")
	}
	return head
}

func assistantImportTruncatedTotal(plan *imports.Plan) int {
	total := 0
	for _, n := range plan.Truncated {
		total += n
	}
	return total
}

// assistantImportSourceLabel renders a source kind the way a person says it.
// Unknown kinds render verbatim rather than failing: enum drift downgrades.
func assistantImportSourceLabel(kind string) string {
	switch kind {
	case imports.SourceLinear:
		return "Linear"
	case imports.SourceJira:
		return "Jira"
	case "":
		return "the source"
	}
	return kind
}

// ---------------------------------------------------------------------------
// import_status
// ---------------------------------------------------------------------------

type assistantImportStatusArgs struct {
	WorkspaceID string `json:"workspace_id"`
	JobID       string `json:"job_id"`
}

func (h *Handler) assistantImportStatus(ctx context.Context, caller assistantCaller, raw json.RawMessage) (json.RawMessage, error) {
	ws, _, err := h.assistantWorkspaceScope(ctx, caller, raw)
	if err != nil {
		return nil, err
	}
	var args assistantImportStatusArgs
	if uerr := json.Unmarshal(raw, &args); uerr != nil {
		return nil, errAssistantBadArgs
	}
	job, err := h.assistantImportJob(ctx, ws, args.JobID)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"job_id":         uuidToString(job.ID),
		"workspace_id":   uuidToString(ws.ID),
		"workspace_slug": ws.Slug,
		"source":         job.Source,
		"status":         job.Status,
		// Terminal is computed by the framework, where an UNKNOWN status is
		// treated as not-terminal: a value this build has never heard of must
		// not make a live job look finished.
		"terminal":    imports.TerminalJobStatus(job.Status),
		"created_at":  assistantImportStamp(job.CreatedAt),
		"started_at":  assistantImportStamp(job.StartedAt),
		"finished_at": assistantImportStamp(job.FinishedAt),
		"totals":      assistantImportDecodeObject(job.Totals),
		"failures":    assistantImportFailures(job.Failures),
		"status_note": "pending / dry_run / awaiting_confirm are before the human's click; running is in flight; " +
			"done / failed / cancelled are terminal. Any other value is one this build does not know — describe it " +
			"generically rather than guessing.",
		"secrets_note": assistantImportSecretsNote,
	}
	if plan, perr := imports.DecodePlan(job.Plan); perr == nil && plan != nil {
		payload["plan"] = assistantImportPlanPayload(plan)
		payload["plan_headline"] = assistantImportSummary(plan, ws)
	}
	return json.Marshal(payload)
}

// assistantImportFailures renders import_job.failures as a bounded list. The
// column is already capped by the applier (Limits.MaxFailures); this caps it
// again for the transcript, because a 200-line failure list spends the
// conversation's context on rows nobody reads.
func assistantImportFailures(blob []byte) []map[string]any {
	if len(blob) == 0 {
		return []map[string]any{}
	}
	var rows []imports.Failure
	if err := json.Unmarshal(blob, &rows); err != nil {
		return []map[string]any{}
	}
	const max = 20
	out := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		if i >= max {
			out = append(out, map[string]any{
				"kind":       "truncated",
				"identifier": "",
				"reason":     fmt.Sprintf("%d more failure(s) are recorded on the job and not listed here", len(rows)-max),
			})
			break
		}
		out = append(out, map[string]any{"kind": row.Kind, "identifier": row.Identifier, "reason": row.Reason})
	}
	return out
}

func assistantImportDecodeObject(blob []byte) map[string]any {
	out := map[string]any{}
	if len(blob) == 0 {
		return out
	}
	if err := json.Unmarshal(blob, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// ---------------------------------------------------------------------------
// Shared plumbing
// ---------------------------------------------------------------------------

// assistantImportPlanPayload renders an ImportPlan for the model.
//
// The ORDER of the keys is not the point (JSON objects are unordered); the
// point is that every honesty field the framework computes travels: `exact`,
// `truncated`, `warnings`, the create/update split, the byte total and the
// skip reasons. A summary that dropped them would let the model report a
// comfortable number, which is the exact failure §3.2 exists to prevent.
func assistantImportPlanPayload(plan *imports.Plan) map[string]any {
	if plan == nil {
		return map[string]any{}
	}
	containers := make([]map[string]any, 0, len(plan.Containers))
	for _, c := range plan.Containers {
		containers = append(containers, map[string]any{
			"name":   c.Name,
			"key":    c.Key,
			"issues": c.Issues,
			"action": c.Action,
		})
	}
	statuses := make([]map[string]any, 0, len(plan.Statuses))
	for _, s := range plan.Statuses {
		statuses = append(statuses, map[string]any{
			"source": s.SourceName, "agora": s.Status, "category": s.SourceCategory, "via": s.Via,
		})
	}
	unmapped := make([]map[string]any, 0, len(plan.UnmappedStatuses))
	for _, s := range plan.UnmappedStatuses {
		unmapped = append(unmapped, map[string]any{"source": s.Name, "issues": s.Issues})
	}
	// Unmatched people FIRST: they are the rows the operator has to act on,
	// and a list that buries them under thirty matched names gets skimmed.
	people := make([]map[string]any, 0, len(plan.Users))
	for _, u := range plan.Users {
		if u.Matched() {
			continue
		}
		people = append(people, map[string]any{
			"name": u.Name, "email": u.Email, "via": u.Via, "matched": false,
		})
	}
	sort.Slice(people, func(i, j int) bool {
		return fmt.Sprint(people[i]["name"]) < fmt.Sprint(people[j]["name"])
	})

	return map[string]any{
		"source":       plan.Source.Kind,
		"source_ref":   plan.Source.Ref,
		"generated_at": plan.GeneratedAt.UTC().Format(time.RFC3339),
		"issues": map[string]any{
			"total": plan.Issues.Total, "create": plan.Issues.Create, "update": plan.Issues.Update,
		},
		"comments": map[string]any{
			"total": plan.Comments.Total, "create": plan.Comments.Create, "update": plan.Comments.Update,
		},
		"containers": containers,
		"iterations": plan.Iterations,
		"labels":     len(plan.Labels),
		"attachments": map[string]any{
			"count": plan.Attachments.Count, "bytes": plan.Attachments.Bytes,
			"skipped": plan.Attachments.Skipped, "skipped_reasons": plan.Attachments.SkippedReasons,
			"unsupported": plan.Attachments.Unsupported,
		},
		"relations": map[string]any{
			"total": plan.Relations.Total, "degraded": plan.Relations.Degraded, "dangling": plan.Relations.Dangling,
		},
		"statuses":          statuses,
		"unmapped_statuses": unmapped,
		"unmatched_users":   plan.UnmatchedUsers,
		"unmatched_people":  people,
		// `exact` false means a headline number is an ESTIMATE. Report it as
		// one; "about 4,800 issues" is the honest phrasing (§4.4).
		"exact":     plan.Exact,
		"truncated": plan.Truncated,
		"warnings":  plan.Warnings,
	}
}

// assistantImportConnection resolves a connection the model named, under the
// workspace gate. The row it returns carries the sealed secret, so it never
// leaves this file.
func (h *Handler) assistantImportConnection(ctx context.Context, ws db.Workspace, ref string) (db.ImportConnection, error) {
	connUUID, err := util.ParseUUID(strings.TrimSpace(ref))
	if err != nil {
		return db.ImportConnection{}, errors.New("connection_id must be an import connection UUID from list_import_connections")
	}
	return h.assistantImportConnectionByUUID(ctx, ws, connUUID)
}

func (h *Handler) assistantImportConnectionByUUID(ctx context.Context, ws db.Workspace, connUUID pgtype.UUID) (db.ImportConnection, error) {
	if !connUUID.Valid {
		return db.ImportConnection{}, errors.New("that import has no connection any more — reconnect the source in " + assistantWhereImport)
	}
	conn, err := h.Queries.GetImportConnectionSecret(ctx, db.GetImportConnectionSecretParams{
		ID:          connUUID,
		WorkspaceID: ws.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.ImportConnection{}, errors.New("there is no import connection with that id in this workspace — list_import_connections shows the ones there are, and a new one is added in " + assistantWhereImport)
		}
		slog.Warn("assistant: read import connection failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.ImportConnection{}, errors.New("could not read that import connection")
	}
	if conn.Source != assistantImportSource {
		return db.ImportConnection{}, fmt.Errorf("that connection is for %q, which this Agora version cannot import from yet (today: %s)",
			conn.Source, assistantImportSource)
	}
	return conn, nil
}

// assistantImportJob resolves a job by id, or falls back to the workspace's
// most recent one when the model passed none — which is what "how is the
// import going?" means in the middle of a conversation.
func (h *Handler) assistantImportJob(ctx context.Context, ws db.Workspace, ref string) (db.ImportJob, error) {
	if trimmed := strings.TrimSpace(ref); trimmed != "" {
		jobUUID, err := util.ParseUUID(trimmed)
		if err != nil {
			return db.ImportJob{}, errors.New("job_id must be an import job UUID, from dry_run_import or confirm_import")
		}
		job, err := h.Queries.GetImportJob(ctx, db.GetImportJobParams{ID: jobUUID, WorkspaceID: ws.ID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.ImportJob{}, errors.New("there is no import with that id in this workspace")
			}
			slog.Warn("assistant: read import job failed", "workspace_id", uuidToString(ws.ID), "error", err)
			return db.ImportJob{}, errors.New("could not read that import")
		}
		return job, nil
	}
	jobs, err := h.Queries.ListImportJobs(ctx, db.ListImportJobsParams{WorkspaceID: ws.ID, Limit: 1})
	if err != nil {
		slog.Warn("assistant: list import jobs failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return db.ImportJob{}, errors.New("could not read the imports for this workspace")
	}
	if len(jobs) == 0 {
		return db.ImportJob{}, errors.New("this workspace has never run an import — start with dry_run_import once a source is connected in " + assistantWhereImport)
	}
	return jobs[0], nil
}

// assistantImportAdapter builds the source adapter for one connection.
//
// This is the ONLY place the plaintext key exists, it exists for the length of
// this function, and it goes straight into the client's own field. It is never
// returned, never logged, and never put in an error: the wrapped error below
// deliberately carries imports.ErrSealKeyUnset's wording rather than anything
// derived from the ciphertext.
func (h *Handler) assistantImportAdapter(conn db.ImportConnection) (imports.Adapter, error) {
	token, err := imports.OpenSecret(conn.SecretEncrypted)
	if err != nil {
		if errors.Is(err, imports.ErrSealKeyUnset) {
			return nil, errors.New("imports are not enabled on this Agora instance — the instance operator has to set the import seal key before a source can be read")
		}
		slog.Warn("assistant: open import secret failed", "connection_id", uuidToString(conn.ID))
		return nil, errors.New("that connection's stored key could not be decrypted — re-enter it in " + assistantWhereImport)
	}
	// The plaintext goes straight into the client's own field and is not kept
	// in a variable this function returns, logs or formats. (Go strings are
	// immutable, so there is no honest "wipe" to perform here — the guarantee
	// is that nothing else ever reads it, which the leak test asserts on the
	// marshaled output of every tool rather than on this line.)
	return linear.NewAdapter(linear.New(linear.Config{
		APIKey: token,
		// base_url is empty for a Linear connection in production; it exists
		// so a self-hosted or stubbed endpoint can be pointed at without a
		// second column.
		Endpoint: strings.TrimSpace(conn.BaseUrl),
	})), nil
}

// assistantImportRunner builds the job runner. Publish is deliberately nil:
// `import:progress` fanout belongs to the HTTP layer that owns the event bus
// wiring, and a nil publisher makes the framework skip publication rather than
// inventing a channel nobody listens on.
func (h *Handler) assistantImportRunner() *imports.Runner {
	return &imports.Runner{
		Jobs:   h.Queries,
		Store:  h.Queries,
		Limits: imports.DefaultLimits(),
	}
}

// assistantImportInFlight turns the framework's refusal into an ANSWER rather
// than an error: "a second import is refused with a pointer at the running
// one" (§3.7) only helps if the pointer reaches the user.
func (h *Handler) assistantImportInFlight(ctx context.Context, ws db.Workspace, err error) (json.RawMessage, error) {
	var inFlight *imports.ErrImportInFlight
	if !errors.As(err, &inFlight) {
		slog.Warn("assistant: open import job failed", "workspace_id", uuidToString(ws.ID), "error", err)
		return nil, errors.New("could not open an import for this workspace")
	}
	payload := map[string]any{
		"import_in_flight": true,
		"job_id":           inFlight.JobID,
		"job_status":       inFlight.Status,
		"workspace_id":     uuidToString(ws.ID),
		"workspace_slug":   ws.Slug,
		"message": fmt.Sprintf("this workspace already has an import in progress (job %s, %s). "+
			"It was NOT cancelled and nothing new was started — read it with import_status, or cancel it in %s before starting another.",
			inFlight.JobID, inFlight.Status, assistantWhereImport),
		"secrets_note": assistantImportSecretsNote,
	}
	if active, ok := h.assistantImportActiveJob(ctx, ws.ID); ok {
		payload["active_job"] = active
	}
	return json.Marshal(payload)
}

// assistantImportScopeOf maps the model's scope object onto imports.Scope.
func assistantImportScopeOf(args *assistantImportScopeArgs) (imports.Scope, error) {
	scope := imports.Scope{}
	if args == nil {
		return scope, nil
	}
	for _, c := range args.Containers {
		if trimmed := strings.TrimSpace(c); trimmed != "" {
			scope.Containers = append(scope.Containers, trimmed)
		}
	}
	if raw := strings.TrimSpace(args.Since); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			// A date is also acceptable — it is what a person says.
			parsed, err = time.Parse("2006-01-02", raw)
			if err != nil {
				return imports.Scope{}, errors.New("since must be a date (YYYY-MM-DD) or an RFC3339 timestamp")
			}
		}
		utc := parsed.UTC()
		scope.Since = &utc
	}
	if args.IncludeArchived != nil {
		scope.IncludeArchived = *args.IncludeArchived
	}
	scope.IncludeComments = args.IncludeComments
	scope.IncludeAttachments = args.IncludeAttachments
	if args.ProvisionUsers != nil {
		scope.ProvisionUsers = *args.ProvisionUsers
	}
	if args.MaxIssues != nil && *args.MaxIssues > 0 {
		scope.MaxIssues = *args.MaxIssues
	}
	return scope, nil
}

func assistantImportStamp(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------------------
// Identity
// ---------------------------------------------------------------------------

// assistantImportIdentityStore is the framework's imports.IdentityStore backed
// by this handler's pool and queries.
//
// It is a deliberate twin of importIdentityStore in import_runner.go (the HTTP
// side of the importer), written independently so the assistant surface does
// not depend on a route's helpers. Once both halves have landed, collapsing
// the two onto one implementation is a one-file follow-up — the seam is
// imports.IdentityStore either way.
//
// Note what it does NOT hold: the operator. imports.ActorResolver is given
// this store and nothing else with which to attribute a row, which is what
// makes §3.3's rule — AN UNMATCHED AUTHOR IS NEVER WRITTEN AS THE IMPORTING
// USER — structural rather than a convention somebody can forget. The Bitrix
// importer fixed exactly this bug once; the framework makes it unfixable.
type assistantImportIdentityStore struct {
	h *Handler
}

func (h *Handler) assistantImportIdentity() imports.IdentityStore {
	return assistantImportIdentityStore{h: h}
}

var _ imports.IdentityStore = assistantImportIdentityStore{}

// UserIDByExternalIdentity is step 1: a link a previous run, or the user
// themselves, already made.
func (s assistantImportIdentityStore) UserIDByExternalIdentity(ctx context.Context, provider, externalID string) (string, error) {
	if strings.TrimSpace(externalID) == "" {
		return "", nil
	}
	return s.h.userIDByExternalIdentity(ctx, provider, externalID)
}

// LinkExternalIdentity records the binding so the NEXT run is step 1. The
// existing helper carries the link-steal guard: a (provider, external_id)
// owned by somebody else is NOT overwritten, and the refusal degrades the
// actor to unresolved rather than failing the run.
func (s assistantImportIdentityStore) LinkExternalIdentity(ctx context.Context, provider, externalID, userID string) error {
	if strings.TrimSpace(externalID) == "" || strings.TrimSpace(userID) == "" {
		return nil
	}
	return s.h.linkExternalIdentity(ctx, provider, externalID, userID)
}

// MemberUserIDByEmail is step 3, and the workspace scope on it is the whole
// safety property: matching against ALL users would let an import bind a
// stranger's account to a name that happens to share an address.
func (s assistantImportIdentityStore) MemberUserIDByEmail(ctx context.Context, workspaceID, email string) (string, error) {
	wanted := strings.ToLower(strings.TrimSpace(email))
	if wanted == "" {
		return "", nil
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return "", errors.New("imports: identity lookup needs a workspace uuid")
	}
	members, err := s.h.Queries.ListMembersWithUser(ctx, wsUUID)
	if err != nil {
		return "", err
	}
	for _, member := range members {
		if strings.ToLower(strings.TrimSpace(member.UserEmail)) == wanted {
			return uuidToString(member.UserID), nil
		}
	}
	return "", nil
}

// EnsureUser is step 4 (provisioning, off by default) and step 5's attribution
// identity. It never touches an existing user's profile.
func (s assistantImportIdentityStore) EnsureUser(ctx context.Context, email, name string) (string, error) {
	address := strings.ToLower(strings.TrimSpace(email))
	if address == "" {
		return "", errors.New("imports: cannot create a user with no address")
	}
	if user, err := s.h.Queries.GetUserByEmail(ctx, address); err == nil {
		return uuidToString(user.ID), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	created, err := s.h.Queries.CreateUser(ctx, db.CreateUserParams{
		Name:      strings.TrimSpace(name),
		Email:     address,
		AvatarUrl: pgtype.Text{},
	})
	if err != nil {
		return "", err
	}
	return uuidToString(created.ID), nil
}

// EnsureMember is idempotent: an existing membership keeps its role, so an
// import can never quietly demote somebody to `member`.
func (s assistantImportIdentityStore) EnsureMember(ctx context.Context, workspaceID, userID, role string) error {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return errors.New("imports: membership needs a workspace uuid")
	}
	userUUID, err := util.ParseUUID(userID)
	if err != nil {
		return errors.New("imports: membership needs a user uuid")
	}
	if _, err := s.h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		UserID:      userUUID,
		WorkspaceID: wsUUID,
	}); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if strings.TrimSpace(role) == "" {
		role = "member"
	}
	_, err = s.h.Queries.CreateMember(ctx, db.CreateMemberParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
		Role:        role,
	})
	return err
}
