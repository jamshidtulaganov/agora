"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  AlertCircle,
  FileCode2,
  FileDiff,
  GitBranch,
  GitCommitHorizontal,
  Loader2,
  RefreshCw,
  Search,
} from "lucide-react";
import { issueArtifactOptions } from "@agora/core/issues/queries";
import {
  parseArtifactChangesResponse,
  parseArtifactFileResponse,
} from "@agora/core/issues/artifact";
import type {
  ArtifactChangedFile,
  IssueArtifactResponse,
} from "@agora/core/types";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Input } from "@agora/ui/components/ui/input";
import {
  NativeSelect,
  NativeSelectOption,
} from "@agora/ui/components/ui/native-select";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { CodeBlock } from "@agora/ui/markdown/CodeBlock";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { parseArtifactFileDiff, type ArtifactDiffLine } from "./artifact-diff";
import { artifactDaemonPost } from "./artifact-daemon-client";

const MAX_VISIBLE_FILES = 500;
const MAX_VISIBLE_LINES = 5_000;

function shortSha(value: string): string {
  return value.slice(0, 8);
}

function pathParts(path: string): { name: string; directory: string } {
  const slash = path.lastIndexOf("/");
  if (slash < 0) return { name: path, directory: "" };
  return { name: path.slice(slash + 1), directory: path.slice(0, slash) };
}

function DiffLine({ line }: { line: ArtifactDiffLine }) {
  const tone =
    line.kind === "addition"
      ? "bg-success/10"
      : line.kind === "deletion"
        ? "bg-destructive/10"
        : line.kind === "hunk"
          ? "bg-brand/10 text-brand"
          : line.kind === "meta"
            ? "bg-muted/40 text-muted-foreground"
            : "";
  const marker = line.kind === "addition" ? "+" : line.kind === "deletion" ? "−" : " ";

  return (
    <div className={cn("grid min-w-max grid-cols-[3.25rem_3.25rem_1.5rem_1fr]", tone)}>
      <span className="select-none border-r px-2 text-right text-muted-foreground/60" aria-hidden>
        {line.oldLine ?? ""}
      </span>
      <span className="select-none border-r px-2 text-right text-muted-foreground/60" aria-hidden>
        {line.newLine ?? ""}
      </span>
      <span className="select-none px-1.5 text-muted-foreground" aria-hidden>
        {line.kind === "hunk" || line.kind === "meta" ? "" : marker}
      </span>
      <span className="whitespace-pre pr-6">{line.content || " "}</span>
    </div>
  );
}

export function artifactLanguage(path: string): string {
  const name = path.split("/").at(-1)?.toLowerCase() ?? "";
  const extension = name.includes(".") ? name.split(".").at(-1) ?? "" : "";
  const byExtension: Record<string, string> = {
    js: "javascript", jsx: "jsx", mjs: "javascript", cjs: "javascript",
    ts: "typescript", tsx: "tsx", json: "json", jsonc: "jsonc",
    go: "go", py: "python", rb: "ruby", php: "php", java: "java",
    css: "css", scss: "scss", less: "less", html: "html", vue: "vue",
    sh: "bash", zsh: "bash", yml: "yaml", yaml: "yaml", sql: "sql",
    md: "markdown", mdx: "mdx", xml: "xml", toml: "toml", ini: "ini",
  };
  if (name === "dockerfile") return "dockerfile";
  if (name === "makefile") return "makefile";
  return byExtension[extension] ?? "text";
}

function SourceLines({ content, path, hiddenLabel }: { content: string; path: string; hiddenLabel: (count: number) => string }) {
  const lines = content.split("\n");
  const visible = lines.slice(0, MAX_VISIBLE_LINES);
  return (
    <>
      <div className="grid min-w-max grid-cols-[4rem_minmax(max-content,1fr)]">
        <div className="select-none border-r border-border/70 bg-muted/15 text-right text-muted-foreground/60 dark:border-white/10 dark:bg-black/10" aria-hidden>
          {visible.map((_, index) => (
            <div key={index} className="h-5 px-3">{index + 1}</div>
          ))}
        </div>
        <CodeBlock
          code={visible.join("\n")}
          language={artifactLanguage(path)}
          mode="minimal"
          className="min-w-max px-3 text-[12px] leading-5 [&_pre]:!whitespace-pre [&_pre]:!break-normal [&_pre]:leading-5"
        />
      </div>
      {lines.length > visible.length && (
        <div className="border-t bg-muted/40 px-4 py-2 text-muted-foreground">
          {hiddenLabel(lines.length - visible.length)}
        </div>
      )}
    </>
  );
}

function ArtifactLoading() {
  return (
    <div className="flex h-full min-h-80 overflow-hidden rounded-lg border" aria-busy="true">
      <div className="w-72 shrink-0 space-y-3 border-r p-3">
        <Skeleton className="h-7 w-full motion-reduce:animate-none" />
        {Array.from({ length: 7 }).map((_, index) => (
          <Skeleton key={index} className="h-6 w-full motion-reduce:animate-none" />
        ))}
      </div>
      <div className="flex-1 space-y-2 p-4">
        <Skeleton className="h-7 w-2/3 motion-reduce:animate-none" />
        <Skeleton className="h-64 w-full motion-reduce:animate-none" />
      </div>
    </div>
  );
}

function ArtifactEmpty({
  data,
  onInspect,
  onRetry,
  error,
}: {
  data?: IssueArtifactResponse;
  onInspect: (stepId: string) => void;
  onRetry: () => void;
  error?: Error | null;
}) {
  const { t } = useT("issues");
  const components = data?.components ?? [];
  const pending = data?.reason === "integration artifact is not ready";

  return (
    <div className="flex min-h-80 items-center justify-center rounded-lg border border-dashed bg-muted/10 px-6 py-12 text-center">
      <div className="max-w-md">
        <span className="mx-auto flex size-9 items-center justify-center rounded-lg border bg-background text-muted-foreground">
          {error ? <AlertCircle className="size-4" aria-hidden /> : <GitCommitHorizontal className="size-4" aria-hidden />}
        </span>
        <h3 className="mt-3 text-sm font-semibold">
          {error
            ? t(($) => $.artifact.load_failed)
            : pending
              ? t(($) => $.artifact.integration_pending_title)
              : t(($) => $.artifact.empty_title)}
        </h3>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
          {error?.message ||
            (pending
              ? t(($) => $.artifact.integration_pending_description)
              : t(($) => $.artifact.empty_description))}
        </p>
        {error && (
          <Button className="mt-3" size="sm" variant="outline" onClick={onRetry}>
            <RefreshCw aria-hidden />
            {t(($) => $.artifact.refresh)}
          </Button>
        )}
        {!error && components.length > 0 && (
          <div className="mt-4 flex flex-wrap justify-center gap-2">
            {components.filter((component) => !component.canonical).map((component) => (
              <Button
                key={component.id}
                size="sm"
                variant="outline"
                onClick={() => onInspect(component.step_id)}
              >
                <GitBranch aria-hidden />
                {component.title}
              </Button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export function ArtifactCodeViewer({
  issueId,
  stepId,
  className,
}: {
  issueId: string;
  stepId?: string;
  className?: string;
}) {
  const { t } = useT("issues");
  const [activeStepId, setActiveStepId] = useState(stepId ?? "");
  const [repoName, setRepoName] = useState("");
  const [selectedPath, setSelectedPath] = useState("");
  const [scope, setScope] = useState<"changed" | "all">("changed");
  const [view, setView] = useState<"diff" | "file">("diff");
  const [search, setSearch] = useState("");

  useEffect(() => setActiveStepId(stepId ?? ""), [stepId]);

  const artifactQuery = useQuery(issueArtifactOptions(issueId, activeStepId || undefined));
  const artifactData = artifactQuery.data;
  const artifact = artifactData?.artifact;
  const changesCapability = artifactData?.capabilities.changes;

  const changesQuery = useQuery({
    queryKey: ["artifact-content", artifact?.id ?? ""],
    queryFn: async () => {
      const raw = await artifactDaemonPost(artifactData?.daemon_url ?? "", "/artifact/changes", {
        capability: changesCapability ?? "",
      });
      const parsed = parseArtifactChangesResponse(raw);
      if (!parsed.artifact_id || parsed.artifact_id !== artifact?.id || parsed.repos.length === 0) {
        throw new Error(t(($) => $.artifact.response_mismatch));
      }
      return parsed;
    },
    enabled: Boolean(artifact?.id && artifactData?.daemon_url && changesCapability),
    staleTime: Number.POSITIVE_INFINITY,
  });

  const repos = useMemo(() => changesQuery.data?.repos ?? [], [changesQuery.data?.repos]);
  const currentRepo = repos.find((repo) => repo.repo === repoName) ?? repos[0];

  useEffect(() => {
    if (repos.length > 0 && !repos.some((repo) => repo.repo === repoName)) {
      setRepoName(repos[0]?.repo ?? "");
    }
  }, [repoName, repos]);

  useEffect(() => {
    if (!currentRepo) return;
    const available = new Set([...currentRepo.files.map((file) => file.path), ...currentRepo.tree]);
    if (!selectedPath || !available.has(selectedPath)) {
      setSelectedPath(currentRepo.files[0]?.path ?? currentRepo.tree[0] ?? "");
    }
  }, [currentRepo, selectedPath]);

  const changedByPath = useMemo(
    () => new Map((currentRepo?.files ?? []).map((file) => [file.path, file])),
    [currentRepo],
  );
  const files = scope === "changed" ? (currentRepo?.files.map((file) => file.path) ?? []) : (currentRepo?.tree ?? []);
  const normalizedSearch = search.trim().toLocaleLowerCase();
  const filteredFiles = files.filter((path) => path.toLocaleLowerCase().includes(normalizedSearch));
  const visibleFiles = filteredFiles.slice(0, MAX_VISIBLE_FILES);
  const selectedChange = changedByPath.get(selectedPath);
  const diffLines = useMemo(
    () => parseArtifactFileDiff(currentRepo?.diff ?? "", selectedPath),
    [currentRepo?.diff, selectedPath],
  );

  const fileCapability = artifactData?.capabilities.file;
  const fileQuery = useQuery({
    queryKey: ["artifact-file", artifact?.id ?? "", currentRepo?.repo ?? "", selectedPath],
    queryFn: async () => {
      const raw = await artifactDaemonPost(artifactData?.daemon_url ?? "", "/artifact/file", {
        capability: fileCapability ?? "",
        repo: currentRepo?.repo ?? "",
        path: selectedPath,
      });
      const parsed = parseArtifactFileResponse(raw);
      if (!parsed.path || parsed.path !== selectedPath || parsed.repo !== currentRepo?.repo) {
        throw new Error(t(($) => $.artifact.response_mismatch));
      }
      return parsed;
    },
    enabled: Boolean(view === "file" && artifact?.id && fileCapability && currentRepo?.repo && selectedPath),
    staleTime: Number.POSITIVE_INFINITY,
  });

  const refresh = async () => {
    await artifactQuery.refetch();
    await changesQuery.refetch();
  };

  if (artifactQuery.isLoading || (artifact && changesQuery.isLoading)) return <ArtifactLoading />;
  const loadError = (artifactQuery.error ?? changesQuery.error) as Error | null;
  if (!artifact || loadError) {
    return (
      <ArtifactEmpty
        data={artifactData}
        error={loadError}
        onInspect={setActiveStepId}
        onRetry={() => void refresh()}
      />
    );
  }

  const headSha = currentRepo?.head_sha ?? artifact.repos[0]?.head_sha ?? "";
  const totalAdditions = currentRepo?.files.reduce((total, file) => total + file.additions, 0) ?? 0;
  const totalDeletions = currentRepo?.files.reduce((total, file) => total + file.deletions, 0) ?? 0;

  return (
    <section className={cn("flex min-h-96 flex-col overflow-hidden rounded-lg border bg-background", className)} aria-label={t(($) => $.artifact.title)}>
      <header className="flex min-h-12 flex-wrap items-center gap-2 border-b px-3 py-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-sm font-semibold">{t(($) => $.artifact.title)}</h3>
            <Badge variant="outline" className={cn("font-normal", artifact.canonical ? "text-success" : "text-warning")}>
              {artifact.canonical
                ? t(($) => $.artifact.canonical)
                : t(($) => $.artifact.worker_branch)}
            </Badge>
          </div>
          <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
            <GitCommitHorizontal className="size-3 shrink-0" aria-hidden />
            <code className="font-mono">{shortSha(headSha)}</code>
            <span aria-hidden>·</span>
            <span>{t(($) => $.artifact.file_count, { count: currentRepo?.files.length ?? 0 })}</span>
            <span className="text-success">+{totalAdditions}</span>
            <span className="text-destructive">−{totalDeletions}</span>
          </div>
        </div>
        {artifactData && artifactData.components.length > 1 && (
          <NativeSelect
            id={`artifact-${issueId}`}
            size="sm"
            value={activeStepId}
            onChange={(event) => setActiveStepId(event.target.value)}
            aria-label={t(($) => $.artifact.select_artifact)}
          >
            {artifactData.ready && <NativeSelectOption value="">{t(($) => $.artifact.canonical)}</NativeSelectOption>}
            {artifactData.components.filter((component) => !component.canonical).map((component) => (
              <NativeSelectOption key={component.id} value={component.step_id}>{component.title}</NativeSelectOption>
            ))}
          </NativeSelect>
        )}
        {repos.length > 1 && (
          <NativeSelect
            size="sm"
            value={currentRepo?.repo ?? ""}
            onChange={(event) => setRepoName(event.target.value)}
            aria-label={t(($) => $.artifact.select_repository)}
          >
            {repos.map((repo) => <NativeSelectOption key={repo.repo} value={repo.repo}>{repo.repo}</NativeSelectOption>)}
          </NativeSelect>
        )}
        <Button size="icon-sm" variant="ghost" onClick={() => void refresh()} aria-label={t(($) => $.artifact.refresh)}>
          <RefreshCw aria-hidden />
        </Button>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-1 md:h-[calc(100vh-11rem)] md:max-h-[52rem] md:min-h-[30rem] md:flex-none md:grid-cols-[18rem_minmax(0,1fr)] md:overflow-hidden">
        <aside
          className="flex min-h-56 min-w-0 flex-col border-b bg-background md:sticky md:top-0 md:h-full md:min-h-0 md:self-start md:overflow-hidden md:border-r md:border-b-0"
          aria-label={t(($) => $.artifact.files)}
        >
          <div className="flex items-center gap-1 border-b p-2">
            <Button size="xs" variant={scope === "changed" ? "secondary" : "ghost"} onClick={() => setScope("changed")}>
              <FileDiff aria-hidden />
              {t(($) => $.artifact.changed)}
            </Button>
            <Button size="xs" variant={scope === "all" ? "secondary" : "ghost"} onClick={() => setScope("all")}>
              <FileCode2 aria-hidden />
              {t(($) => $.artifact.all_files)}
            </Button>
          </div>
          <div className="relative border-b p-2">
            <Search className="pointer-events-none absolute top-1/2 left-4 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
            <Input
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder={t(($) => $.artifact.search_placeholder)}
              aria-label={t(($) => $.artifact.search_placeholder)}
              className="h-7 pl-7 text-xs"
            />
          </div>
          <div className="min-h-0 flex-1 overflow-auto py-1">
            {visibleFiles.map((path) => {
              const stat = changedByPath.get(path);
              const { name, directory } = pathParts(path);
              return (
                <button
                  key={path}
                  type="button"
                  title={path}
                  onClick={() => {
                    setSelectedPath(path);
                    if (!stat) setView("file");
                  }}
                  className={cn(
                    "flex w-full min-w-0 items-center gap-2 px-3 py-1.5 text-left text-xs outline-none hover:bg-muted/60 focus-visible:bg-muted focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-inset",
                    selectedPath === path && "bg-muted text-foreground",
                  )}
                >
                  {stat ? <FileDiff className="size-3.5 shrink-0 text-muted-foreground" aria-hidden /> : <FileCode2 className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />}
                  <span className="min-w-0 flex-1 truncate">
                    <span>{name}</span>
                    {directory && <span className="ml-1 text-muted-foreground">{directory}/</span>}
                  </span>
                  {stat && <FileStats file={stat} />}
                </button>
              );
            })}
            {filteredFiles.length === 0 && (
              <p className="px-3 py-6 text-center text-xs text-muted-foreground">{t(($) => $.artifact.no_files_match)}</p>
            )}
            {filteredFiles.length > visibleFiles.length && (
              <p className="border-t px-3 py-2 text-[11px] text-muted-foreground">
                {t(($) => $.artifact.file_limit, { count: visibleFiles.length, total: filteredFiles.length })}
              </p>
            )}
          </div>
        </aside>

        <main className="flex min-h-64 min-w-0 flex-col md:h-full md:min-h-0 md:overflow-hidden">
          {selectedPath ? (
            <>
              <div className="flex min-h-10 items-center gap-2 border-b px-3 py-1.5">
                <code className="min-w-0 flex-1 truncate font-mono text-xs" title={selectedPath}>{selectedPath}</code>
                <div className="flex items-center rounded-lg border bg-muted/20 p-0.5">
                  <Button size="xs" variant={view === "diff" ? "secondary" : "ghost"} disabled={!selectedChange} onClick={() => setView("diff")}>
                    {t(($) => $.artifact.diff)}
                  </Button>
                  <Button size="xs" variant={view === "file" ? "secondary" : "ghost"} onClick={() => setView("file")}>
                    {t(($) => $.artifact.full_file)}
                  </Button>
                </div>
              </div>
              <div className="min-h-0 flex-1 overflow-auto bg-white font-mono text-[12px] leading-5 text-[#1f2328] dark:bg-[#282c34] dark:text-[#abb2bf]" translate="no">
                {view === "diff" ? (
                  diffLines.length > 0 ? (
                    <>
                      {diffLines.slice(0, MAX_VISIBLE_LINES).map((line, index) => <DiffLine key={index} line={line} />)}
                      {diffLines.length > MAX_VISIBLE_LINES && <div className="border-t bg-muted/40 px-4 py-2 text-muted-foreground">{t(($) => $.artifact.lines_hidden)}</div>}
                    </>
                  ) : (
                    <p className="px-4 py-10 text-center font-sans text-xs text-muted-foreground">{t(($) => $.artifact.no_diff)}</p>
                  )
                ) : fileQuery.isLoading ? (
                  <div className="flex items-center justify-center gap-2 py-12 font-sans text-xs text-muted-foreground"><Loader2 className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden />{t(($) => $.artifact.loading_file)}</div>
                ) : fileQuery.error ? (
                  <p className="px-4 py-10 text-center font-sans text-xs text-destructive">{(fileQuery.error as Error).message}</p>
                ) : fileQuery.data?.binary ? (
                  <p className="px-4 py-10 text-center font-sans text-xs text-muted-foreground">{t(($) => $.artifact.binary_file)}</p>
                ) : (
                  <>
                    {fileQuery.data?.truncated && <div className="border-b bg-warning/10 px-4 py-2 font-sans text-xs text-warning">{t(($) => $.artifact.truncated_file)}</div>}
                    <SourceLines
                      content={fileQuery.data?.content ?? ""}
                      path={selectedPath}
                      hiddenLabel={(count) => t(($) => $.artifact.more_lines_hidden, { count })}
                    />
                  </>
                )}
              </div>
            </>
          ) : (
            <div className="flex flex-1 items-center justify-center text-xs text-muted-foreground">{t(($) => $.artifact.select_file)}</div>
          )}
        </main>
      </div>
    </section>
  );
}

function FileStats({ file }: { file: ArtifactChangedFile }) {
  return (
    <span className="flex shrink-0 gap-1 font-mono text-[10px]" aria-label={`+${file.additions}, -${file.deletions}`}>
      {file.additions > 0 && <span className="text-success">+{file.additions}</span>}
      {file.deletions > 0 && <span className="text-destructive">−{file.deletions}</span>}
    </span>
  );
}
