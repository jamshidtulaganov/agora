"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CheckCircle2,
  ExternalLink,
  FlaskConical,
  Loader2,
  Play,
  RefreshCw,
  ShieldCheck,
  Square,
  XCircle,
} from "lucide-react";
import { issueArtifactOptions } from "@agora/core/issues/queries";
import {
  parseArtifactChecksResponse,
  parseArtifactPreviewResponse,
} from "@agora/core/issues/artifact";
import type {
  ArtifactChecksResponse,
  ArtifactPreviewResponse,
  IssueArtifactResponse,
} from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { artifactDaemonPost, artifactPreviewURL } from "./artifact-daemon-client";

function selectedArtifactRepo(data: IssueArtifactResponse | undefined, repoName: string) {
  const repos = data?.artifact?.repos ?? [];
  return repos.find((repo) => repo.repo === repoName) ?? repos[0];
}

function RuntimeUnavailable({ kind }: { kind: "preview" | "checks" }) {
  const { t } = useT("issues");
  return (
    <div className="flex min-h-80 items-center justify-center rounded-lg border border-dashed bg-muted/10 px-6 py-12 text-center">
      <div className="max-w-md">
        <span className="mx-auto flex size-9 items-center justify-center rounded-lg border bg-background text-muted-foreground">
          {kind === "preview" ? <Play className="size-4" aria-hidden /> : <ShieldCheck className="size-4" aria-hidden />}
        </span>
        <h3 className="mt-3 text-sm font-semibold">{t(($) => $.dev_workspace.exact_head_pending)}</h3>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
          {t(($) => $.dev_workspace.exact_head_pending_description)}
        </p>
      </div>
    </div>
  );
}

function RuntimeSkeleton() {
  return <Skeleton className="min-h-80 w-full rounded-lg motion-reduce:animate-none" />;
}

function previewKey(artifactId: string, repo: string, capability: string) {
  // The capability token is part of the key: a rotated token must produce a
  // fresh fetch instead of serving a status cached under the stale grant.
  return ["artifact-preview", artifactId, repo, capability] as const;
}

export function ArtifactPreviewPanel({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const queryClient = useQueryClient();
  const [iframeKey, setIframeKey] = useState(0);
  const [repoName, setRepoName] = useState("");
  const artifactQuery = useQuery(issueArtifactOptions(issueId));
  const data = artifactQuery.data;
  const artifact = data?.artifact;
  const capability = data?.capabilities.preview;
  const selectedRepo = selectedArtifactRepo(data, repoName);
  // A live local artifact has no frozen repos — the daemon points preview at the
  // developer's own dev server, so a selected repo isn't required.
  const isLive = artifact?.kind === "local_directory";
  const key = previewKey(artifact?.id ?? "", selectedRepo?.repo ?? "", capability ?? "");

  const statusQuery = useQuery({
    queryKey: key,
    queryFn: async () => {
      const raw = await artifactDaemonPost(data?.daemon_url ?? "", "/artifact/preview/status", {
        capability: capability ?? "",
        repo: selectedRepo?.repo ?? "",
      });
      const parsed = parseArtifactPreviewResponse(raw);
      if (!parsed.artifact_id || parsed.artifact_id !== artifact?.id) {
        throw new Error(t(($) => $.artifact.response_mismatch));
      }
      return parsed;
    },
    enabled: Boolean(artifact?.id && capability && data?.daemon_url),
    refetchInterval: (query) => query.state.data?.running === true ? 3_000 : false,
  });

  const start = useMutation({
    mutationFn: async () => {
      const raw = await artifactDaemonPost(data?.daemon_url ?? "", "/artifact/preview", {
        capability: capability ?? "",
        repo: selectedRepo?.repo ?? "",
      });
      const parsed = parseArtifactPreviewResponse(raw);
      if (!parsed.artifact_id || parsed.artifact_id !== artifact?.id) {
        throw new Error(t(($) => $.artifact.response_mismatch));
      }
      return parsed;
    },
    onSuccess: (preview) => {
      queryClient.setQueryData(key, preview);
      setIframeKey((value) => value + 1);
    },
  });

  const stop = useMutation({
    mutationFn: () => artifactDaemonPost(data?.daemon_url ?? "", "/artifact/preview/stop", {
      capability: capability ?? "",
      repo: selectedRepo?.repo ?? "",
    }),
    onSuccess: () => {
      queryClient.setQueryData<ArtifactPreviewResponse>(key, {
        artifact_id: artifact?.id ?? "",
        running: false,
        needs_command: false,
      });
    },
  });

  if (artifactQuery.isLoading) return <RuntimeSkeleton />;
  if (!artifact || !capability || !data?.daemon_url || (!selectedRepo && !isLive)) return <RuntimeUnavailable kind="preview" />;

  const preview = statusQuery.data;
  const url = preview ? artifactPreviewURL(data.daemon_url, preview) : "";
  const error = start.error ?? statusQuery.error;
  const sha = selectedRepo?.head_sha ?? "";

  return (
    <section className="flex min-h-[34rem] flex-col overflow-hidden rounded-lg border bg-background" aria-label={t(($) => $.dev_workspace.preview)}>
      <header className="flex min-h-12 flex-wrap items-center gap-2 border-b px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">{t(($) => $.dev_workspace.preview_title)}</h3>
            <Badge variant="outline" className="font-mono font-normal">{sha.slice(0, 8)}</Badge>
          </div>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{t(($) => $.dev_workspace.detached_runtime)}</p>
        </div>
        {(artifact.repos?.length ?? 0) > 1 && (
          <NativeSelect
            size="sm"
            value={selectedRepo?.repo ?? ""}
            onChange={(event) => setRepoName(event.target.value)}
            aria-label={t(($) => $.artifact.select_repository)}
          >
            {artifact.repos.map((repo) => <NativeSelectOption key={repo.repo} value={repo.repo}>{repo.repo}</NativeSelectOption>)}
          </NativeSelect>
        )}
        {preview?.running ? (
          <>
            <Button size="icon-sm" variant="ghost" onClick={() => setIframeKey((value) => value + 1)} aria-label={t(($) => $.dev_workspace.reload_preview)}>
              <RefreshCw aria-hidden />
            </Button>
            {url && (
              <Button size="icon-sm" variant="ghost" render={<a href={url} target="_blank" rel="noreferrer noopener" />} aria-label={t(($) => $.dev_workspace.open_preview)}>
                <ExternalLink aria-hidden />
              </Button>
            )}
            <Button size="sm" variant="outline" disabled={stop.isPending} onClick={() => stop.mutate()}>
              {stop.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Square aria-hidden />}
              {t(($) => $.dev_workspace.stop_preview)}
            </Button>
          </>
        ) : (
          <Button size="sm" disabled={start.isPending} onClick={() => start.mutate()}>
            {start.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Play aria-hidden />}
            {start.isPending ? t(($) => $.dev_workspace.starting_preview) : t(($) => $.dev_workspace.start_preview)}
          </Button>
        )}
      </header>
      <div className="relative flex min-h-0 flex-1 items-center justify-center bg-muted/10">
        {preview?.running && url ? (
          <iframe
            key={iframeKey}
            src={url}
            title={t(($) => $.dev_workspace.preview_frame_title)}
            className="h-full min-h-[30rem] w-full border-0 bg-background"
            sandbox="allow-downloads allow-forms allow-modals allow-popups allow-presentation allow-scripts"
            referrerPolicy="no-referrer"
          />
        ) : start.isPending ? (
          <div className="max-w-sm px-6 text-center">
            <Loader2 className="mx-auto size-5 animate-spin text-brand motion-reduce:animate-none" aria-hidden />
            <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.preparing_preview)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.preparing_preview_description)}</p>
          </div>
        ) : preview?.needs_command ? (
          <div className="max-w-sm px-6 text-center">
            <FlaskConical className="mx-auto size-5 text-muted-foreground" aria-hidden />
            <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.preview_not_configured)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.preview_not_configured_description)}</p>
          </div>
        ) : preview?.error || error ? (
          <div className="max-w-lg px-6 text-center">
            <XCircle className="mx-auto size-5 text-destructive" aria-hidden />
            <p className="mt-3 text-sm font-medium text-destructive">{preview?.error || (error as Error).message}</p>
            {preview?.log && <pre className="mt-3 max-h-40 overflow-auto rounded-lg border bg-muted/30 p-3 text-left font-mono text-[11px] whitespace-pre-wrap text-muted-foreground" translate="no">{preview.log}</pre>}
          </div>
        ) : (
          <div className="max-w-sm px-6 text-center">
            <Play className="mx-auto size-5 text-muted-foreground" aria-hidden />
            <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.preview_idle)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.preview_idle_description)}</p>
          </div>
        )}
      </div>
    </section>
  );
}

function checksKey(artifactId: string, repo: string, capability: string) {
  // Same rotation rule as previewKey: a fresh capability grant must never be
  // answered from a cache entry produced under the previous token.
  return ["artifact-checks", artifactId, repo, capability] as const;
}

function cleanOutput(value: string): string {
  const ansiSequence = new RegExp(`${String.fromCharCode(27)}\\[[0-?]*[ -/]*[@-~]`, "g");
  return value.replace(ansiSequence, "");
}

export function ArtifactChecksPanel({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const [repoName, setRepoName] = useState("");
  const artifactQuery = useQuery(issueArtifactOptions(issueId));
  const data = artifactQuery.data;
  const artifact = data?.artifact;
  const capability = data?.capabilities.checks;
  const selectedRepo = selectedArtifactRepo(data, repoName);
  const checksQuery = useQuery({
    queryKey: checksKey(artifact?.id ?? "", selectedRepo?.repo ?? "", capability ?? ""),
    queryFn: async () => {
      const raw = await artifactDaemonPost(data?.daemon_url ?? "", "/artifact/checks", {
        capability: capability ?? "",
        repo: selectedRepo?.repo ?? "",
      });
      const parsed = parseArtifactChecksResponse(raw);
      if (!parsed.artifact_id || parsed.artifact_id !== artifact?.id || (parsed.head_sha && parsed.head_sha !== selectedRepo?.head_sha)) {
        throw new Error(t(($) => $.artifact.response_mismatch));
      }
      return parsed;
    },
    enabled: false,
    staleTime: Number.POSITIVE_INFINITY,
  });

  if (artifactQuery.isLoading) return <RuntimeSkeleton />;
  if (!artifact || !capability || !data?.daemon_url || !selectedRepo) return <RuntimeUnavailable kind="checks" />;

  const result = checksQuery.data;
  const sha = selectedRepo?.head_sha ?? "";
  return (
    <section className="overflow-hidden rounded-lg border bg-background" aria-label={t(($) => $.dev_workspace.checks)}>
      <header className="flex min-h-12 flex-wrap items-center gap-2 border-b px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">{t(($) => $.dev_workspace.checks_title)}</h3>
            <Badge variant="outline" className="font-mono font-normal">{sha.slice(0, 8)}</Badge>
          </div>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{t(($) => $.dev_workspace.checks_description)}</p>
        </div>
        {(artifact.repos?.length ?? 0) > 1 && (
          <NativeSelect
            size="sm"
            value={selectedRepo?.repo ?? ""}
            onChange={(event) => setRepoName(event.target.value)}
            aria-label={t(($) => $.artifact.select_repository)}
          >
            {artifact.repos.map((repo) => <NativeSelectOption key={repo.repo} value={repo.repo}>{repo.repo}</NativeSelectOption>)}
          </NativeSelect>
        )}
        <Button size="sm" disabled={checksQuery.isFetching} onClick={() => void checksQuery.refetch()}>
          {checksQuery.isFetching ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <ShieldCheck aria-hidden />}
          {checksQuery.isFetching
            ? t(($) => $.dev_workspace.running_checks)
            : result
              ? t(($) => $.dev_workspace.rerun_checks)
              : t(($) => $.dev_workspace.run_checks)}
        </Button>
      </header>
      <div className="min-h-72 p-4">
        {checksQuery.isFetching ? (
          <div className="flex min-h-64 items-center justify-center text-center">
            <div>
              <Loader2 className="mx-auto size-5 animate-spin text-brand motion-reduce:animate-none" aria-hidden />
              <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.checks_running_title)}</p>
              <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.checks_running_description)}</p>
            </div>
          </div>
        ) : checksQuery.error ? (
          <div className="flex min-h-64 items-center justify-center text-center text-destructive">
            <div><XCircle className="mx-auto size-5" aria-hidden /><p className="mt-3 text-sm font-medium">{(checksQuery.error as Error).message}</p></div>
          </div>
        ) : result ? (
          <ChecksResult result={result} />
        ) : (
          <div className="flex min-h-64 items-center justify-center text-center">
            <div className="max-w-sm">
              <ShieldCheck className="mx-auto size-5 text-muted-foreground" aria-hidden />
              <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.checks_idle)}</p>
              <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.checks_idle_description)}</p>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

function ChecksResult({ result }: { result: ArtifactChecksResponse }) {
  const { t } = useT("issues");
  if (result.needs_command) {
    return (
      <div className="flex min-h-64 items-center justify-center text-center">
        <div className="max-w-sm">
          <FlaskConical className="mx-auto size-5 text-muted-foreground" aria-hidden />
          <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.checks_not_configured)}</p>
          <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.checks_not_configured_description)}</p>
        </div>
      </div>
    );
  }
  const passed = result.passed === true;
  return (
    <div className={cn("rounded-lg border p-4", passed ? "border-success/30 bg-success/5" : "border-destructive/30 bg-destructive/5")}>
      <div className="flex items-start gap-3">
        {passed ? <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-success" aria-hidden /> : <XCircle className="mt-0.5 size-5 shrink-0 text-destructive" aria-hidden />}
        <div className="min-w-0 flex-1">
          <h4 className="text-sm font-semibold">{passed ? t(($) => $.dev_workspace.checks_passed) : t(($) => $.dev_workspace.checks_failed)}</h4>
          <p className="mt-1 text-xs text-muted-foreground">
            {result.command ? <code className="font-mono">{result.command}</code> : t(($) => $.dev_workspace.detected_checks)}
            {result.exit_code !== undefined && <span> · {t(($) => $.dev_workspace.exit_code, { code: result.exit_code })}</span>}
          </p>
          {(result.output || result.error) && (
            <details className="mt-4 rounded-lg border bg-background/70">
              <summary className="cursor-pointer px-3 py-2 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring/50">
                {t(($) => $.dev_workspace.view_check_output)}
              </summary>
              <pre className="max-h-96 overflow-auto border-t p-3 font-mono text-[11px] leading-5 whitespace-pre-wrap text-muted-foreground" translate="no">
                {cleanOutput(result.output || result.error || "")}
              </pre>
            </details>
          )}
        </div>
      </div>
    </div>
  );
}
