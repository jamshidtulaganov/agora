package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jamshidtulaganov/agora/server/internal/config"
	"github.com/jamshidtulaganov/agora/server/internal/events"
	"github.com/jamshidtulaganov/agora/server/internal/imports"
	"github.com/jamshidtulaganov/agora/server/internal/imports/linear"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
	"github.com/jamshidtulaganov/agora/server/pkg/protocol"
)

// Server-side seams for the tracker importer (docs/importers-plan.md §3).
//
// The imports package is deliberately free of the database, the storage layer
// and the websocket hub: it declares interfaces and the handler — the only
// layer that owns all three — supplies them. This file is that supply, and
// nothing else. The HTTP surface lives in import_connections.go (the sealed
// credential) and import_jobs.go (dry run, confirm, status, cancel).
//
// Four seams:
//
//   - newImportAdapter — connection + decrypted token -> vendor adapter.
//   - importIdentityStore — the five-step actor resolver's view of the
//     identity tables (§3.3).
//   - importAttachmentSink — where a file streamed out of the source lands
//     (§3.6). Copying needs BOTH halves; without a sink the applier reports
//     the files as skipped instead of dropping them quietly.
//   - publishImportProgress — throttled `import:progress` on the event bus.

// ---- configuration ---------------------------------------------------------

// Registry keys. AGORA_IMPORT_SECRET_KEY (the seal key) is not here because it
// is a secret: it is read through imports.SecretBox, never through config.
const (
	cfgImportEnabled                = "AGORA_IMPORT_ENABLED"
	cfgImportMaxIssues              = "AGORA_IMPORT_MAX_ISSUES"
	cfgImportMaxAttachmentMB        = "AGORA_IMPORT_MAX_ATTACHMENT_MB"
	cfgImportMaxTotalAttachmentMB   = "AGORA_IMPORT_MAX_TOTAL_ATTACHMENT_MB"
	cfgImportMaxAttachmentsPerIssue = "AGORA_IMPORT_MAX_ATTACHMENTS_PER_ISSUE"
	cfgImportMaxFailures            = "AGORA_IMPORT_MAX_FAILURES"
	cfgImportDryRunWaitSeconds      = "AGORA_IMPORT_DRY_RUN_WAIT_SECONDS"
	cfgImportJobTimeoutMinutes      = "AGORA_IMPORT_JOB_TIMEOUT_MINUTES"
)

// requireImportEnabled is the kill switch every import endpoint runs first.
// Off means 503, not 404: an operator who turned the feature off should see
// "disabled here", and a client should not have to guess whether the route
// exists on this build.
func requireImportEnabled(w http.ResponseWriter) bool {
	if !config.Bool(cfgImportEnabled) {
		writeError(w, http.StatusServiceUnavailable, "tracker import is disabled on this server (AGORA_IMPORT_ENABLED)")
		return false
	}
	return true
}

// writeImportSealError translates the one error that must never degrade into a
// plaintext write. It reports whether it handled err — the exact posture of
// figma_credential.go / git_credential.go: without the key there is no
// half-working mode, there is 503.
func writeImportSealError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, imports.ErrSealKeyUnset) {
		writeError(w, http.StatusServiceUnavailable,
			"import credentials are not configured on this server (AGORA_IMPORT_SECRET_KEY unset)")
		return true
	}
	return false
}

// importLimits resolves the per-run budgets from the registry, falling back to
// the framework's conservative defaults. They are surfaced in the dry-run plan
// rather than enforced silently (§3.6).
func importLimits() imports.Limits {
	d := imports.DefaultLimits()
	const mb = 1 << 20
	return imports.Limits{
		MaxAttachmentBytes:      int64(config.Int(cfgImportMaxAttachmentMB, int(d.MaxAttachmentBytes/mb))) * mb,
		MaxTotalAttachmentBytes: int64(config.Int(cfgImportMaxTotalAttachmentMB, int(d.MaxTotalAttachmentBytes/mb))) * mb,
		MaxAttachmentsPerIssue:  config.Int(cfgImportMaxAttachmentsPerIssue, d.MaxAttachmentsPerIssue),
		MaxFailures:             config.Int(cfgImportMaxFailures, d.MaxFailures),
	}
}

// importDryRunWait is how long the dry-run endpoint holds the request open
// hoping to answer with the plan itself before falling back to "poll the job".
func importDryRunWait() time.Duration {
	return time.Duration(config.Int(cfgImportDryRunWaitSeconds, 20)) * time.Second
}

// importJobTimeout bounds a background run. A walk that has not finished in
// this long is cancelled and recorded as such, so a wedged job cannot hold the
// workspace's one in-flight slot forever.
func importJobTimeout() time.Duration {
	return time.Duration(config.Int(cfgImportJobTimeoutMinutes, 120)) * time.Minute
}

// ---- adapter seam ----------------------------------------------------------

// newImportAdapter builds the vendor adapter for a connection. It is a var so
// a test can substitute a stub adapter (or a real Linear adapter pointed at an
// httptest server) — the Runner takes the Adapter as an argument, so this one
// seam covers probe, dry run and apply alike.
var newImportAdapter = buildImportAdapter

func buildImportAdapter(conn db.ImportConnection, token string) (imports.Adapter, error) {
	switch conn.Source {
	case imports.SourceLinear:
		// Phase 1 is the personal API key against Linear's single GraphQL
		// endpoint. base_url stays empty for Linear by design (it is Jira's
		// site URL): honouring an operator-supplied host here would be a way
		// to aim a customer's API key at somebody else's server.
		return linear.NewAdapter(linear.New(linear.Config{APIKey: token})), nil
	default:
		return nil, fmt.Errorf("import source %q is not supported by this server", conn.Source)
	}
}

// importAdapterFor loads a connection, decrypts its token server-side and
// builds the adapter. The plaintext exists only inside the adapter's HTTP
// client from here on: it is never returned, logged or published.
func (h *Handler) importAdapterFor(ctx context.Context, wsID, connID pgtype.UUID) (db.ImportConnection, imports.Adapter, error) {
	conn, err := h.Queries.GetImportConnectionSecret(ctx, db.GetImportConnectionSecretParams{
		ID:          connID,
		WorkspaceID: wsID,
	})
	if err != nil {
		return db.ImportConnection{}, nil, err
	}
	token, err := imports.OpenSecret(conn.SecretEncrypted)
	if err != nil {
		return conn, nil, err
	}
	adapter, err := newImportAdapter(conn, token)
	if err != nil {
		return conn, nil, err
	}
	return conn, adapter, nil
}

// ---- identity seam ---------------------------------------------------------

// importIdentityStore is the resolver's view of the identity tables. It speaks
// string uuids because user_external_identity is deliberately outside the
// generated set (raw pgx in external_identity.go) — see imports.IdentityStore.
type importIdentityStore struct{ h *Handler }

var _ imports.IdentityStore = importIdentityStore{}

func (s importIdentityStore) UserIDByExternalIdentity(ctx context.Context, provider, externalID string) (string, error) {
	return s.h.userIDByExternalIdentity(ctx, provider, externalID)
}

// LinkExternalIdentity reuses the link-steal guard verbatim: a (provider,
// external_id) already owned by a DIFFERENT user is left alone and the refusal
// surfaces as an error, which the importer degrades to "unresolved actor"
// rather than failing the run.
func (s importIdentityStore) LinkExternalIdentity(ctx context.Context, provider, externalID, userID string) error {
	return s.h.linkExternalIdentity(ctx, provider, externalID, userID)
}

// MemberUserIDByEmail is scoped to the workspace by contract — a global email
// match would bind a stranger into somebody else's import.
func (s importIdentityStore) MemberUserIDByEmail(ctx context.Context, workspaceID, email string) (string, error) {
	email = strings.TrimSpace(email)
	if workspaceID == "" || email == "" {
		return "", nil
	}
	var userID string
	err := s.h.DB.QueryRow(ctx, `
		SELECT u.id::text
		FROM "user" u
		JOIN member m ON m.user_id = u.id
		WHERE m.workspace_id = $1::uuid AND lower(u.email) = lower($2)
		LIMIT 1`, workspaceID, email).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return userID, nil
}

func (s importIdentityStore) EnsureUser(ctx context.Context, email, name string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return "", errors.New("imports: cannot ensure a user without an email")
	}
	existing, err := s.h.Queries.GetUserByEmail(ctx, email)
	if err == nil {
		return uuidToString(existing.ID), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		name = email
	}
	created, err := s.h.Queries.CreateUser(ctx, db.CreateUserParams{Name: name, Email: email})
	if err != nil {
		// Another goroutine (or another replica) created the same address
		// between the read and the write. That is the success case, not a
		// failure: re-read rather than erroring the import.
		if isUniqueViolation(err) {
			if again, reErr := s.h.Queries.GetUserByEmail(ctx, email); reErr == nil {
				return uuidToString(again.ID), nil
			}
		}
		return "", err
	}
	return uuidToString(created.ID), nil
}

func (s importIdentityStore) EnsureMember(ctx context.Context, workspaceID, userID, role string) error {
	wsUUID, err := parseUUIDErr(workspaceID)
	if err != nil {
		return err
	}
	userUUID, err := parseUUIDErr(userID)
	if err != nil {
		return err
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
	if _, err := s.h.Queries.CreateMember(ctx, db.CreateMemberParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
		Role:        role,
	}); err != nil && !isUniqueViolation(err) {
		return err
	}
	return nil
}

// ---- attachment seam -------------------------------------------------------

// importAttachmentSink stores one file the adapter streamed out of the source.
// It exists in the handler because this is the layer that owns Storage; the
// applier only knows the interface.
type importAttachmentSink struct {
	h           *Handler
	workspaceID pgtype.UUID
	// resolver supplies the uploader: the SOURCE'S attribution identity, not
	// the operator. An imported file was not uploaded by whoever pressed
	// Confirm (§3.3), and resolving it lazily means a run with no attachments
	// never creates the account at all.
	resolver *imports.ActorResolver
	maxBytes int64
}

var _ imports.AttachmentSink = (*importAttachmentSink)(nil)

// StoreAttachment buffers up to the per-file cap and uploads. Storage.Upload
// takes bytes rather than a stream, so the cap is enforced here with a
// LimitReader: a source that lies about Size cannot turn one file into an
// unbounded allocation.
func (s *importAttachmentSink) StoreAttachment(ctx context.Context, issueID string, att imports.Attachment, body io.Reader) error {
	if s.h.Storage == nil {
		return errors.New("attachment storage is not configured on this server")
	}
	issueUUID, err := parseUUIDErr(issueID)
	if err != nil {
		return fmt.Errorf("attachment target issue: %w", err)
	}
	uploader, err := s.uploader(ctx)
	if err != nil {
		return err
	}
	limit := s.maxBytes
	if limit <= 0 {
		limit = imports.DefaultLimits().MaxAttachmentBytes
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("file is larger than the %d MB per-file cap and stays in the source", limit/(1<<20))
	}
	filename := strings.TrimSpace(att.Title)
	if filename == "" {
		filename = "attachment"
	}
	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generate attachment id: %w", err)
	}
	// Same key layout as UploadFile / the Bitrix importer.
	key := "workspaces/" + uuidToString(s.workspaceID) + "/" + id.String() + path.Ext(filename)
	link, err := s.h.Storage.Upload(ctx, key, data, att.ContentType, filename)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	if _, err := s.h.Queries.CreateAttachment(ctx, db.CreateAttachmentParams{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		WorkspaceID:  s.workspaceID,
		UploaderType: "member",
		UploaderID:   uploader,
		Filename:     filename,
		Url:          link,
		ContentType:  att.ContentType,
		SizeBytes:    int64(len(data)),
		IssueID:      issueUUID,
	}); err != nil {
		return fmt.Errorf("create attachment row: %w", err)
	}
	return nil
}

// uploader resolves the source's attribution user. attachment.uploader_id is
// NOT NULL, so a run that cannot resolve one reports the file as a FAILURE
// (listed, with the source URL still on the issue's linkage blob) rather than
// attributing somebody's screen recording to the operator.
func (s *importAttachmentSink) uploader(ctx context.Context) (pgtype.UUID, error) {
	if s.resolver == nil {
		return pgtype.UUID{}, errors.New("import attachments have no attribution identity")
	}
	id, err := s.resolver.ImportIdentity(ctx)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("import attribution identity: %w", err)
	}
	uploader, err := parseUUIDErr(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("import attribution identity: %w", err)
	}
	return uploader, nil
}

// ---- progress seam ---------------------------------------------------------

// publishImportProgress puts one throttled tick on the event bus. The Runner
// already decides WHEN (every N rows and every 2s, whichever is slower); this
// only decides WHERE, and the answer is the workspace room — the same fanout
// every other workspace event uses, never a synchronous write-path broadcast.
func (h *Handler) publishImportProgress(workspaceID, jobID string, p imports.Progress) {
	if h.Bus == nil || workspaceID == "" {
		return
	}
	h.Bus.Publish(events.Event{
		Type:        protocol.EventImportProgress,
		WorkspaceID: workspaceID,
		ActorType:   "system",
		Payload: protocol.ImportProgressPayload{
			JobID:   jobID,
			Phase:   p.Phase,
			Kind:    p.Kind,
			Done:    p.Done,
			Total:   p.Total,
			Message: p.Message,
		},
	})
}

// importRunner builds the per-request Runner. It holds nothing another request
// would want, so one is constructed per call rather than cached.
func (h *Handler) importRunner(limits imports.Limits) *imports.Runner {
	return &imports.Runner{
		Jobs:    h.Queries,
		Store:   h.Queries,
		Publish: h.publishImportProgress,
		Limits:  limits,
	}
}

// ---- mapping + resolver ----------------------------------------------------

// importMappingFor layers the request's overrides on top of the workspace's
// stored ones and hands the result to the adapter's defaults.
//
// The request overrides go through imports.ParseOverrides by way of a
// settings-shaped document rather than being trusted as given: that is the one
// place that drops an unknown Agora status, so a typo in a mapping edit cannot
// reach the applier and produce a 23514 mid-run.
func importMappingFor(settings []byte, source string, defaults imports.Defaults, extra *imports.Overrides) *imports.Mapping {
	overrides := imports.ParseOverrides(settings, source)
	if extra != nil {
		overrides = mergeImportOverrides(overrides, sanitizeImportOverrides(*extra, source))
	}
	return imports.NewMapping(source, defaults, overrides)
}

// sanitizeImportOverrides runs caller-supplied overrides through exactly the
// validation the stored ones get.
func sanitizeImportOverrides(in imports.Overrides, source string) imports.Overrides {
	doc, err := json.Marshal(map[string]any{
		imports.SettingsKeyMapping: map[string]imports.Overrides{source: in},
	})
	if err != nil {
		return imports.Overrides{}
	}
	return imports.ParseOverrides(doc, source)
}

// mergeImportOverrides layers `over` on top of `base`, key by key.
func mergeImportOverrides(base, over imports.Overrides) imports.Overrides {
	base.Status = mergeStringMaps(base.Status, over.Status)
	base.Priority = mergeStringMaps(base.Priority, over.Priority)
	base.Containers = mergeStringMaps(base.Containers, over.Containers)
	return base
}

func mergeStringMaps(base, over map[string]string) map[string]string {
	if len(over) == 0 {
		return base
	}
	if base == nil {
		base = map[string]string{}
	}
	for k, v := range over {
		base[k] = v
	}
	return base
}

// importResolverFor builds the five-step actor resolver for one run. Note what
// it is NOT given: the operator. An unmatched author becomes the source's
// attribution identity, never whoever pressed Confirm.
func (h *Handler) importResolverFor(settings []byte, source, workspaceID string, scope imports.Scope, dryRun bool) *imports.ActorResolver {
	return imports.NewActorResolver(importIdentityStore{h: h}, imports.ResolverConfig{
		Source:      source,
		WorkspaceID: workspaceID,
		Aliases:     imports.ParseAliases(settings),
		Provision:   scope.ProvisionUsers,
		DryRun:      dryRun,
	})
}
