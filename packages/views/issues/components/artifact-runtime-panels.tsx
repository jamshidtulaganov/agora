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
import { api } from "@agora/core/api";
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
import { isDesktopShell } from "../../platform";
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

function previewHost(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

// StandingServerPreview embeds a deployed, standing QA target directly — the
// developer's own dev server (user_dev_server) or the project's qa_smoke_url,
// resolved server-side and confirmed embeddable. It has a live database and is
// always running, so there is no build / start / stop chrome: this is the
// answer for projects whose Docker auto-detect would otherwise boot a
// disposable, empty-database container that no login works against.
function StandingServerPreview({ url }: { url: string }) {
  const { t } = useT("issues");
  const [iframeKey, setIframeKey] = useState(0);
  return (
    <section className="flex h-[calc(100dvh-9.5rem)] min-h-[34rem] flex-col overflow-hidden rounded-lg border bg-background" aria-label={t(($) => $.dev_workspace.preview)}>
      <header className="flex min-h-12 flex-wrap items-center gap-2 border-b px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">{t(($) => $.dev_workspace.preview_title)}</h3>
            <Badge variant="secondary" className="h-5 text-[10px] font-normal">
              {t(($) => $.dev_workspace.preview_dev_server_label)}
            </Badge>
          </div>
          <p className="mt-0.5 truncate text-[11px] text-muted-foreground" title={url}>{previewHost(url)}</p>
        </div>
        <Button size="icon-sm" variant="ghost" onClick={() => setIframeKey((value) => value + 1)} aria-label={t(($) => $.dev_workspace.reload_preview)}>
          <RefreshCw aria-hidden />
        </Button>
        <Button size="icon-sm" variant="ghost" render={<a href={url} target="_blank" rel="noreferrer noopener" />} aria-label={t(($) => $.dev_workspace.open_preview)}>
          <ExternalLink aria-hidden />
        </Button>
      </header>
      <div className="relative flex min-h-0 flex-1 items-center justify-center bg-muted/10 p-3 sm:p-4">
        <div className="flex h-full max-h-[48rem] w-full max-w-none flex-col overflow-hidden rounded-xl border-[6px] border-foreground/15 bg-background shadow-xl">
          <div className="flex h-5 shrink-0 items-center justify-center border-b border-foreground/10 bg-muted/70" aria-hidden>
            <span className="size-1.5 rounded-full bg-foreground/25" />
          </div>
          <iframe
            key={iframeKey}
            src={url}
            title={t(($) => $.dev_workspace.preview_frame_title)}
            className="min-h-0 w-full flex-1 border-0 bg-background"
            sandbox={previewSandbox(url)}
            referrerPolicy="no-referrer"
          />
        </div>
      </div>
    </section>
  );
}

function previewKey(artifactId: string, repo: string, capability: string) {
  // The capability token is part of the key: a rotated token must produce a
  // fresh fetch instead of serving a status cached under the stale grant.
  return ["artifact-preview", artifactId, repo, capability] as const;
}

function previewSandbox(url: string): string {
  const permissions = [
    "allow-downloads",
    "allow-forms",
    "allow-modals",
    "allow-popups",
    "allow-presentation",
    "allow-scripts",
  ];

  // Desktop previews run on a separate localhost origin. Preserving that
  // origin is required for real applications that bootstrap from cookies,
  // localStorage, or same-origin asset requests. Never grant it to the web
  // app's same-origin daemon proxy: scripts + same-origin would let an
  // untrusted preview escape the sandbox and reach the parent application.
  if (typeof window !== "undefined") {
    try {
      const previewURL = new URL(url, window.location.href);
      if (/^https?:$/.test(previewURL.protocol) && previewURL.origin !== window.location.origin) {
        permissions.push("allow-same-origin");
      }
    } catch {
      // Keep the strict sandbox when the daemon returns an invalid URL.
    }
  }

  return permissions.join(" ");
}

export function ArtifactPreviewPanel({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const queryClient = useQueryClient();
  const [iframeKey, setIframeKey] = useState(0);
  const [repoName, setRepoName] = useState("");
  const artifactQuery = useQuery(issueArtifactOptions(issueId));
  // A standing external QA target (the developer's dev server, resolved via
  // dev_apps → user_dev_server → qa_smoke_url) wins over the daemon/Docker
  // artifact preview whenever it is embeddable: it carries a live database and
  // is reachable from the hosted web app, where the daemon-proxied Docker
  // preview is not. Always-200 endpoint; url:"" (or not embeddable) falls
  // through to the daemon chain below, so projects without a dev server are
  // unaffected.
  const externalQuery = useQuery({
    queryKey: ["issue-qa-preview-url", issueId],
    queryFn: () => api.getIssueQAPreviewURL(issueId),
    staleTime: 30_000,
  });
  const data = artifactQuery.data;
  const artifact = data?.artifact;
  const capability = data?.capabilities.preview;
  // When a standing dev server takes over the panel, the daemon preview
  // status/start/stop must go quiet — otherwise it would keep polling (and
  // could cold-boot a Docker container) behind the embedded external frame.
  // The desktop shell runs with webSecurity:false, so a cross-origin
  // frame-ancestors CSP on the dev server is not enforced there — a target
  // the server-side probe marks non-embeddable (because the CSP scopes framing
  // to specific origins, not "*") still frames fine in the desktop app. On the
  // web the CSP IS enforced, so honor the probe's verdict.
  const externalURL = externalQuery.data?.url ?? "";
  const externalActive = Boolean(externalURL) && (externalQuery.data?.embeddable === true || isDesktopShell());
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
    enabled: Boolean(artifact?.id && capability && data?.daemon_url) && !externalActive,
    refetchInterval: (query) => query.state.data?.running === true
      ? query.state.data.ready === true ? 3_000 : 1_000
      : false,
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

  if (artifactQuery.isLoading || externalQuery.isLoading) return <RuntimeSkeleton />;
  // Prefer the standing dev server when one resolves and can be framed
  // (embeddable on web; always in the desktop shell — see externalActive).
  if (externalActive) {
    return <StandingServerPreview url={externalURL} />;
  }
  if (!artifact || !capability || !data?.daemon_url || (!selectedRepo && !isLive)) return <RuntimeUnavailable kind="preview" />;

  const preview = statusQuery.data;
  const url = preview ? artifactPreviewURL(data.daemon_url, preview) : "";
  const error = start.error ?? statusQuery.error;
  const sha = selectedRepo?.head_sha ?? "";
  const configurationLabel = preview?.configuration_source === "project"
    ? t(($) => $.dev_workspace.preview_project_config)
    : preview?.configuration_source === "project_resource"
      ? t(($) => $.dev_workspace.preview_resource_config)
      : preview?.configuration_source === "project_repository"
        ? t(($) => $.dev_workspace.preview_repository_config)
      : t(($) => $.dev_workspace.preview_auto_detect);

  return (
    <section className="flex h-[calc(100dvh-9.5rem)] min-h-[34rem] flex-col overflow-hidden rounded-lg border bg-background" aria-label={t(($) => $.dev_workspace.preview)}>
      <header className="flex min-h-12 flex-wrap items-center gap-2 border-b px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">{t(($) => $.dev_workspace.preview_title)}</h3>
            <Badge variant="outline" className="font-mono font-normal">{sha.slice(0, 8)}</Badge>
          </div>
          <p className="mt-0.5 text-[11px] text-muted-foreground">{t(($) => $.dev_workspace.detached_runtime)}</p>
          {preview && (
            <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-1.5">
              <Badge variant="secondary" className="h-5 text-[10px] font-normal">
                {configurationLabel}
              </Badge>
              {preview.command && (
                <code className="max-w-full truncate rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground" title={preview.command}>
                  {preview.command}
                </code>
              )}
            </div>
          )}
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
            {!isLive && (
              <Button size="sm" variant="outline" disabled={stop.isPending} onClick={() => stop.mutate()}>
                {stop.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Square aria-hidden />}
                {t(($) => $.dev_workspace.stop_preview)}
              </Button>
            )}
          </>
        ) : (
          <Button size="sm" disabled={start.isPending} onClick={() => start.mutate()}>
            {start.isPending ? <Loader2 className="animate-spin motion-reduce:animate-none" aria-hidden /> : <Play aria-hidden />}
            {start.isPending ? t(($) => $.dev_workspace.starting_preview) : t(($) => $.dev_workspace.start_preview)}
          </Button>
        )}
      </header>
      <div className="relative flex min-h-0 flex-1 items-center justify-center bg-muted/10 p-3 sm:p-4">
        {preview?.running && preview.ready !== false && url ? (
          <div className="flex h-full max-h-[48rem] w-full max-w-none flex-col overflow-hidden rounded-xl border-[6px] border-foreground/15 bg-background shadow-xl">
            <div className="flex h-5 shrink-0 items-center justify-center border-b border-foreground/10 bg-muted/70" aria-hidden>
              <span className="size-1.5 rounded-full bg-foreground/25" />
            </div>
            <iframe
              key={iframeKey}
              src={url}
              title={t(($) => $.dev_workspace.preview_frame_title)}
              className="min-h-0 w-full flex-1 border-0 bg-background"
              sandbox={previewSandbox(url)}
              referrerPolicy="no-referrer"
            />
          </div>
        ) : start.isPending || (preview?.running && !preview.ready) ? (
          <div className="w-full max-w-2xl px-6 text-center" aria-live="polite">
            <Loader2 className="mx-auto size-5 animate-spin text-brand motion-reduce:animate-none" aria-hidden />
            <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.preparing_preview)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.preparing_preview_description)}</p>
            {preview?.command && (
              <code className="mt-3 block truncate rounded-md border bg-background px-2 py-1.5 text-[11px] text-muted-foreground" title={preview.command}>
                {preview.command}
              </code>
            )}
            {preview?.log && (
              <pre className="mt-3 max-h-48 overflow-auto rounded-lg border bg-[#f6f8fa] p-3 text-left font-mono text-[11px] leading-4 whitespace-pre-wrap text-[#24292f] dark:border-white/10 dark:bg-[#21252b] dark:text-[#abb2bf]" translate="no">
                {preview.log}
              </pre>
            )}
          </div>
        ) : preview?.needs_command ? (
          <div className="max-w-sm px-6 text-center">
            <FlaskConical className="mx-auto size-5 text-muted-foreground" aria-hidden />
            <p className="mt-3 text-sm font-medium">{t(($) => $.dev_workspace.preview_not_configured)}</p>
            <p className="mt-1 text-xs text-muted-foreground">{t(($) => $.dev_workspace.preview_not_configured_description)}</p>
            <p className="mt-2 text-[11px] text-muted-foreground">{t(($) => $.dev_workspace.preview_config_hint)}</p>
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
