"use client";

import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { RefreshCw, Trash2 } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import {
  NativeSelect,
  NativeSelectOption,
} from "@agora/ui/components/ui/native-select";
import { useAuthStore } from "@agora/core/auth";
import { useWorkspaceId } from "@agora/core/hooks";
import { memberListOptions } from "@agora/core/workspace/queries";
import {
  IMPORT_SOURCES,
  type ImportConnection,
  type ImportJob,
  type ImportPlan,
  formatImportBytes,
  importConnectionsOptions,
  importJobOptions,
  importTotalOf,
  isTerminalImportStatus,
  useCancelImport,
  useCreateImportConnection,
  useDeleteImportConnection,
  useDryRunImport,
  useProbeImportConnection,
  useStartImport,
} from "@agora/core/imports";
import { useT } from "../../i18n";

// Settings → Integrations → Import: connect a tracker, preview the migration,
// read the plan, then run it.
//
// The shape of this panel is the shape of the product decision behind it
// (docs/importers-plan.md §4): CONNECT, SURVEY, READ, APPLY. The survey writes
// nothing, so the Apply button only appears once a plan exists — there is no
// path through this UI that imports anything the user has not seen counted.
//
// Two rules are visible in the markup rather than left to review:
//
//   - The key is submitted ONCE and never rendered again. It lives in a
//     password field, is cleared the moment the mutation settles, and nothing
//     in the connection row can display it (the server does not return it).
//   - THE BAD NEWS COMES FIRST. Unreachable rows, unmatched people and
//     over-budget files render above the headline counts, and a plan whose
//     counts are estimates says "about" on every one of them.
export function ImportSection() {
  const { t } = useT("settings");
  const wsId = useWorkspaceId();
  const user = useAuthStore((s) => s.user);

  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const role = members.find((m) => m.user_id === user?.id)?.role ?? "";
  const canManage = role === "owner" || role === "admin";

  const { data: connections = [] } = useQuery({
    ...importConnectionsOptions(wsId),
    enabled: !!wsId,
  });

  const [label, setLabel] = useState("");
  const [secret, setSecret] = useState("");
  const [teams, setTeams] = useState("");
  const [connectionId, setConnectionId] = useState("");
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [jobId, setJobId] = useState("");

  const createConnection = useCreateImportConnection(wsId);
  const probeConnection = useProbeImportConnection(wsId);
  const deleteConnection = useDeleteImportConnection(wsId);
  const dryRun = useDryRunImport(wsId);
  const startImport = useStartImport(wsId);
  const cancelImport = useCancelImport(wsId);

  // The job is polled only while it is live; importJobOptions stops the
  // interval at a terminal status and keeps polling anything it does not
  // recognise, because an unknown status is not proof that a job finished.
  const { data: job } = useQuery({
    ...importJobOptions(wsId, jobId),
    enabled: !!wsId && !!jobId,
  });

  const selected =
    connections.find((c) => c.id === connectionId) ?? connections[0] ?? null;
  const running = !!job && !isTerminalImportStatus(job.status) && job.status !== "awaiting_confirm";

  const scope = () => {
    const containers = teams
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean);
    return containers.length > 0 ? { containers } : {};
  };

  const connect = async () => {
    if (!secret.trim() || createConnection.isPending) return;
    try {
      await createConnection.mutateAsync({
        source: IMPORT_SOURCES[0],
        label: label.trim() || "Linear",
        secret: secret.trim(),
      });
      // Cleared here rather than in a `finally`: the value must not survive a
      // failed save in a React state tree either.
      setSecret("");
      setLabel("");
      toast.success(t(($) => $.imports.toast_connected));
    } catch (e) {
      setSecret("");
      toast.error(e instanceof Error ? e.message : t(($) => $.imports.toast_connect_failed));
    }
  };

  const preview = async () => {
    if (!selected || dryRun.isPending) return;
    try {
      const result = await dryRun.mutateAsync({
        connection_id: selected.id,
        scope: scope(),
      });
      setPlan(result.plan);
      if (result.job_id) setJobId(result.job_id);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.imports.toast_preview_failed));
    }
  };

  const apply = async () => {
    if (!selected || !plan || startImport.isPending) return;
    try {
      const result = await startImport.mutateAsync({
        connection_id: selected.id,
        scope: scope(),
      });
      if (result.job_id) setJobId(result.job_id);
      toast.success(t(($) => $.imports.toast_started));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : t(($) => $.imports.toast_start_failed));
    }
  };

  return (
    <div className="space-y-4">
      <p className="text-xs text-muted-foreground">{t(($) => $.imports.description)}</p>

      {connections.length > 0 ? (
        <ul className="space-y-2">
          {connections.map((connection) => (
            <li
              key={connection.id}
              className="flex flex-wrap items-center gap-2 rounded-md border border-border px-3 py-2 text-sm"
            >
              <span className="min-w-0 flex-1 truncate font-medium">
                {connection.label || connection.source}
              </span>
              <ProbeBadge status={connection.probe_status} />
              {canManage ? (
                <>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 shrink-0"
                    aria-label={t(($) => $.imports.recheck)}
                    disabled={probeConnection.isPending}
                    onClick={() => probeConnection.mutate(connection.id)}
                  >
                    <RefreshCw className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 shrink-0"
                    aria-label={t(($) => $.imports.remove)}
                    disabled={deleteConnection.isPending}
                    onClick={() => deleteConnection.mutate(connection.id)}
                  >
                    <Trash2 className="h-3.5 w-3.5 text-destructive" />
                  </Button>
                </>
              ) : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-xs text-muted-foreground">{t(($) => $.imports.empty)}</p>
      )}

      {canManage ? (
        <div className="space-y-2 rounded-md border border-border p-3">
          <p className="text-xs font-medium">{t(($) => $.imports.connect_title)}</p>
          <div className="flex flex-wrap gap-2">
            <NativeSelect
              aria-label={t(($) => $.imports.source_label)}
              value={IMPORT_SOURCES[0]}
              onChange={() => undefined}
            >
              {/* eslint-disable-next-line i18next/no-literal-string -- Linear is a brand name; the glossary says brands are never translated */}
              <NativeSelectOption value="linear">Linear</NativeSelectOption>
            </NativeSelect>
            <Input
              className="w-40"
              placeholder={t(($) => $.imports.label_placeholder)}
              value={label}
              onChange={(e) => setLabel(e.target.value)}
            />
            <Input
              className="min-w-0 flex-1"
              type="password"
              autoComplete="off"
              placeholder={t(($) => $.imports.key_placeholder)}
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
            />
            <Button size="sm" onClick={connect} disabled={!secret.trim() || createConnection.isPending}>
              {t(($) => $.imports.connect)}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{t(($) => $.imports.key_hint)}</p>
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">{t(($) => $.imports.admin_only)}</p>
      )}

      {canManage && connections.length > 0 ? (
        <div className="space-y-2 rounded-md border border-border p-3">
          <p className="text-xs font-medium">{t(($) => $.imports.scope_title)}</p>
          <div className="flex flex-wrap gap-2">
            {connections.length > 1 ? (
              <NativeSelect
                aria-label={t(($) => $.imports.connection_label)}
                value={selected?.id ?? ""}
                onChange={(e) => {
                  setConnectionId(e.target.value);
                  setPlan(null);
                }}
              >
                {connections.map((connection: ImportConnection) => (
                  <NativeSelectOption key={connection.id} value={connection.id}>
                    {connection.label || connection.source}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            ) : null}
            <Input
              className="min-w-0 flex-1"
              placeholder={t(($) => $.imports.teams_placeholder)}
              value={teams}
              onChange={(e) => setTeams(e.target.value)}
            />
            <Button size="sm" variant="outline" onClick={preview} disabled={dryRun.isPending}>
              {dryRun.isPending ? t(($) => $.imports.previewing) : t(($) => $.imports.preview)}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{t(($) => $.imports.scope_hint)}</p>
        </div>
      ) : null}

      {plan ? <ImportPlanView plan={plan} /> : null}

      {plan && canManage ? (
        <div className="space-y-2 rounded-md border border-border p-3">
          <p className="text-sm">
            {t(($) => $.imports.apply_sentence, {
              issues: plan.issues.create,
              updates: plan.issues.update,
              projects: plan.containers.length,
            })}
          </p>
          <Button size="sm" onClick={apply} disabled={startImport.isPending || running}>
            {t(($) => $.imports.apply)}
          </Button>
        </div>
      ) : null}

      {job && jobId ? (
        <ImportJobView
          job={job}
          onCancel={running ? () => cancelImport.mutate(jobId) : undefined}
        />
      ) : null}
    </div>
  );
}

/**
 * The migration report.
 *
 * Order is the whole design: what the key could not see, who could not be
 * matched and what will not be copied render ABOVE the headline counts. A
 * report that leads with "240 issues found" and buries "12 we cannot see" is
 * the fastest way to lose a team in week two (§4.4).
 */
function ImportPlanView({ plan }: { plan: ImportPlan }) {
  const { t } = useT("settings");
  const unreachable = Object.values(plan.truncated ?? {}).reduce((sum, n) => sum + n, 0);
  const unmatched = plan.users.filter((u) => !u.user_id || u.via === "import_identity");
  // An estimate is labelled as one everywhere it appears, not once at the top.
  const count = (n: number) =>
    plan.exact ? String(n) : t(($) => $.imports.about_count, { count: n });

  return (
    <div className="space-y-3 rounded-md border border-border p-3" data-testid="import-plan">
      {unreachable > 0 || unmatched.length > 0 || plan.attachments.skipped > 0 ? (
        <div className="space-y-1 rounded-md bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
          {unreachable > 0 ? (
            <p>{t(($) => $.imports.plan_unreachable, { count: unreachable })}</p>
          ) : null}
          {unmatched.length > 0 ? (
            <p>{t(($) => $.imports.plan_unmatched, { count: unmatched.length })}</p>
          ) : null}
          {plan.attachments.skipped > 0 ? (
            <p>{t(($) => $.imports.plan_files_skipped, { count: plan.attachments.skipped })}</p>
          ) : null}
        </div>
      ) : null}

      <dl className="grid grid-cols-2 gap-2 text-sm sm:grid-cols-4">
        <Stat label={t(($) => $.imports.plan_issues)} value={count(plan.issues.total)} />
        <Stat label={t(($) => $.imports.plan_updates)} value={String(plan.issues.update)} />
        <Stat label={t(($) => $.imports.plan_comments)} value={count(plan.comments.total)} />
        <Stat label={t(($) => $.imports.plan_projects)} value={String(plan.containers.length)} />
      </dl>

      {plan.containers.length > 0 ? (
        <ul className="space-y-1 text-xs text-muted-foreground">
          {plan.containers.map((container) => (
            <li key={container.external_id || container.name} className="truncate">
              {container.name} · {t(($) => $.imports.plan_container_issues, { count: container.issues })} ·{" "}
              {container.action === "link_project"
                ? t(($) => $.imports.plan_container_link)
                : t(($) => $.imports.plan_container_create)}
            </li>
          ))}
        </ul>
      ) : null}

      {unmatched.length > 0 ? (
        <div className="space-y-1">
          <p className="text-xs font-medium">{t(($) => $.imports.plan_people_title)}</p>
          <ul className="space-y-1 text-xs text-muted-foreground" data-testid="import-unmatched">
            {unmatched.map((person) => (
              <li key={person.external_id || person.email} className="truncate">
                {person.name || person.email || person.external_id}
                {person.email ? ` · ${person.email}` : ""}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {plan.unmapped_statuses.length > 0 ? (
        <div className="space-y-1">
          <p className="text-xs font-medium">{t(($) => $.imports.plan_unmapped_title)}</p>
          <ul className="space-y-1 text-xs text-muted-foreground">
            {plan.unmapped_statuses.map((status) => (
              <li key={status.name} className="truncate">
                {status.name} · {t(($) => $.imports.plan_container_issues, { count: status.issues })}
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      {plan.attachments.count > 0 || plan.attachments.skipped > 0 ? (
        <p className="text-xs text-muted-foreground">
          {t(($) => $.imports.plan_attachments, {
            count: plan.attachments.count,
            size: formatImportBytes(plan.attachments.bytes),
          })}
        </p>
      ) : null}

      {plan.warnings.length > 0 ? (
        <ul className="space-y-1 text-xs text-muted-foreground">
          {plan.warnings.map((warning) => (
            <li key={warning}>{warning}</li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

/** The receipt: what a run actually did, failures included. */
function ImportJobView({ job, onCancel }: { job: ImportJob; onCancel?: () => void }) {
  const { t } = useT("settings");
  const created = importTotalOf(job, "created");
  const updated = importTotalOf(job, "updated");
  const failed = importTotalOf(job, "failed");

  return (
    <div className="space-y-2 rounded-md border border-border p-3" data-testid="import-job">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">
          <ImportStatusText status={job.status} />
        </span>
        {onCancel ? (
          <Button variant="ghost" size="sm" className="ml-auto h-7" onClick={onCancel}>
            {t(($) => $.imports.cancel)}
          </Button>
        ) : null}
      </div>

      <dl className="grid grid-cols-3 gap-2 text-sm">
        <Stat label={t(($) => $.imports.receipt_created)} value={String(created)} />
        <Stat label={t(($) => $.imports.receipt_updated)} value={String(updated)} />
        <Stat label={t(($) => $.imports.receipt_failed)} value={String(failed)} />
      </dl>

      {job.failures.length > 0 ? (
        <ul className="space-y-1 text-xs text-muted-foreground" data-testid="import-failures">
          {job.failures.map((failure, index) => (
            <li key={`${failure.kind}-${failure.identifier}-${index}`} className="truncate">
              {failure.kind}
              {failure.identifier ? ` ${failure.identifier}` : ""} · {failure.reason}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="font-medium">{value}</dd>
    </div>
  );
}

/**
 * A job status, in words. The switch has a DEFAULT branch on purpose: the
 * status column is free-form so a new posture does not need a migration, and
 * an unknown value must render as a generic "working" line rather than as
 * nothing at all (CLAUDE.md: enum drift downgrades, it never crashes).
 */
function ImportStatusText({ status }: { status: string }) {
  const { t } = useT("settings");
  switch (status) {
    case "done":
      return t(($) => $.imports.status_done);
    case "failed":
      return t(($) => $.imports.status_failed);
    case "cancelled":
      return t(($) => $.imports.status_cancelled);
    case "running":
      return t(($) => $.imports.status_running);
    case "awaiting_confirm":
    case "dry_run":
    case "pending":
      return t(($) => $.imports.status_pending);
    default:
      return t(($) => $.imports.status_unknown, { status });
  }
}

/** The probe verdict on a stored key. An empty verdict means "never checked",
 * which is neither good news nor bad and must not be rendered as either. */
function ProbeBadge({ status }: { status: string }) {
  const { t } = useT("settings");
  if (status === "ok") {
    return (
      <span className="shrink-0 rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
        {t(($) => $.imports.probe_ok)}
      </span>
    );
  }
  if (status === "invalid" || status === "unreachable") {
    return (
      <span className="shrink-0 rounded bg-destructive/15 px-1.5 py-0.5 text-[10px] font-medium text-destructive">
        {status === "invalid"
          ? t(($) => $.imports.probe_invalid)
          : t(($) => $.imports.probe_unreachable)}
      </span>
    );
  }
  return (
    <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
      {t(($) => $.imports.probe_unchecked)}
    </span>
  );
}
