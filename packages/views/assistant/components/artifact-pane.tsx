"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, ChevronDown, Copy, Download, History, X } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import {
  assistantArtifactOptions,
  assistantArtifactRevisionOptions,
  assistantArtifactRevisionsOptions,
  assistantSessionArtifactListOptions,
} from "@agora/core/assistant";
import type { AssistantArtifact, AssistantArtifactRevisionSummary } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@agora/ui/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { copyText } from "@agora/ui/lib/clipboard";
import { useT, useTimeAgo } from "../../i18n";
import { artifactExport } from "../lib/artifact-export";
import { ArtifactBody } from "./artifact-body";
import { ArtifactPinAction } from "./artifact-pin-action";
import { artifactKindIcon, useArtifactKindLabel } from "./artifact-card";

interface ArtifactPaneProps {
  artifactId: string;
  onClose: () => void;
  expanded?: boolean;
  onEdit?: () => void;
  /** Session whose artifacts fill the switcher; defaults to the artifact's own. */
  sessionId?: string | null;
  /** Switches the pane to a sibling artifact. Without it the header title is
   *  static — surfaces that drive their own selection (the library) pass
   *  nothing and keep their list as the switcher. */
  onSwitchArtifact?: (artifactId: string) => void;
  /** The run in flight is writing to this artifact right now. */
  isUpdating?: boolean;
  /** Explicit width in px, from the workbench divider. */
  width?: number;
}

/**
 * Right split pane of the assistant page — the Claude-style artifact viewer.
 * The transcript column shrinks beside it; it is never a modal, because the
 * whole point is iterating on the artifact while still talking to the model.
 *
 * The backend for these endpoints ships separately, so every failure path
 * here is a shape this build will really see: a 404 (not deployed yet), a
 * drifted response (EMPTY artifact from parseWithFallback), an unknown kind,
 * or a malformed spec. All four degrade quietly; none of them blanks the page.
 */
export function ArtifactPane({
  artifactId,
  onClose,
  expanded,
  onEdit,
  sessionId,
  onSwitchArtifact,
  isUpdating,
  width,
}: ArtifactPaneProps) {
  const { t } = useT("assistant");
  const { t: labels } = useT("layout");
  const [view, setView] = useState<"preview" | "code">("preview");
  // null = the live artifact. A number selects an immutable past revision,
  // which the pane then renders read-only.
  const [viewVersion, setViewVersion] = useState<number | null>(null);
  useEffect(() => {
    setView("preview");
    setViewVersion(null);
  }, [artifactId]);
  const kindLabel = useArtifactKindLabel();
  const { data, isLoading, isError, refetch } = useQuery(assistantArtifactOptions(artifactId));

  // `id === ""` is the EMPTY_ASSISTANT_ARTIFACT fallback — a 200 whose body
  // failed schema validation. Same user-visible outcome as a 404.
  const artifact = data && data.id ? data : null;
  const unavailable = !isLoading && (isError || !artifact);
  const Icon = artifactKindIcon(artifact?.kind ?? "");

  // History is a progressive enhancement: an older backend 404s both reads and
  // the header simply keeps the plain version badge it has always had.
  const revisionsQuery = useQuery({
    ...assistantArtifactRevisionsOptions(artifactId),
    enabled: !!artifactId && !!artifact,
  });
  const revisions = (revisionsQuery.data ?? [])
    .filter((entry) => entry.version > 0)
    .sort((a, b) => b.version - a.version);
  const hasHistory = revisions.length > 1;

  const revisionQuery = useQuery(assistantArtifactRevisionOptions(artifactId, viewVersion));
  const revision = revisionQuery.data && revisionQuery.data.version > 0 ? revisionQuery.data : null;
  const isHistorical = viewVersion !== null && !!artifact;
  // Everything downstream — body, copy, download — acts on this: the past
  // body while a version is selected, the live artifact otherwise.
  const shown: AssistantArtifact | null =
    isHistorical && artifact
      ? revision
        ? {
            ...artifact,
            content: revision.content,
            version: revision.version,
            title: revision.title || artifact.title,
          }
        : null
      : artifact;
  const title = artifact?.title.trim() || t(($) => $.artifact.untitled);

  return (
    <aside
      className={cn(
        "flex min-w-0 flex-col border-l bg-background",
        expanded ? "flex-1" : "shrink-0",
        !expanded && !width && "w-[40%] min-w-[320px] max-w-[760px]",
      )}
      style={!expanded && width ? { width } : undefined}
    >
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <div className="flex min-w-0 flex-1 flex-col">
          {onSwitchArtifact ? (
            <ArtifactSwitcher
              sessionId={sessionId || artifact?.session_id || ""}
              openArtifactId={artifactId}
              title={title}
              onSelect={onSwitchArtifact}
            />
          ) : (
            <span className="truncate text-sm font-medium">{title}</span>
          )}
          {artifact && (
            <span className="flex min-w-0 items-center gap-1 text-xs text-muted-foreground">
              {hasHistory ? (
                <>
                  <span className="truncate">{kindLabel(artifact.kind)}</span>
                  <span aria-hidden>·</span>
                  <VersionPicker
                    revisions={revisions}
                    shownVersion={shown?.version ?? artifact.version}
                    latestVersion={artifact.version}
                    onSelect={(version) =>
                      setViewVersion(version >= artifact.version ? null : version)
                    }
                  />
                </>
              ) : (
                <span className="truncate">
                  {t(($) => $.artifact.meta, {
                    kind: kindLabel(artifact.kind),
                    version: artifact.version,
                  })}
                </span>
              )}
            </span>
          )}
        </div>
        {isUpdating && <UpdatingBadge />}
        {/* Publish action. The pane only ever renders for the artifact's
            owner today, so no extra gating here — see ArtifactPinAction. */}
        {artifact && <ArtifactPinAction artifactId={artifact.id} />}
        {shown && <CopyContentButton content={shown.content} />}
        {shown && (
          <PaneAction
            label={labels(($) => $.artifacts_library.download)}
            onClick={() => downloadArtifact(shown)}
          >
            <Download />
          </PaneAction>
        )}
        <PaneAction label={t(($) => $.artifact.close)} onClick={onClose}>
          <X />
        </PaneAction>
      </div>

      {isHistorical && artifact && (
        <div
          role="status"
          className="flex items-center gap-2 border-b bg-muted/40 px-3 py-1.5 text-xs text-muted-foreground"
        >
          <History className="size-3.5 shrink-0" />
          <span className="min-w-0 flex-1 truncate">
            {t(($) => $.artifact.viewing_version, {
              version: viewVersion,
              latest: artifact.version,
            })}
          </span>
          <Button size="sm" variant="ghost" className="h-6 px-2" onClick={() => setViewVersion(null)}>
            {t(($) => $.artifact.back_to_latest)}
          </Button>
        </div>
      )}

      {artifact && <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <div className="flex gap-1" role="group" aria-label={labels(($) => $.artifacts_library.view)}>
          <Button size="sm" variant={view === "preview" ? "secondary" : "ghost"} aria-pressed={view === "preview"} onClick={() => setView("preview")}>{labels(($) => $.artifacts_library.preview)}</Button>
          <Button size="sm" variant={view === "code" ? "secondary" : "ghost"} aria-pressed={view === "code"} onClick={() => setView("code")}>{labels(($) => $.artifacts_library.code)}</Button>
        </div>
        {/* Editing means editing the live artifact — never a past version. */}
        {onEdit && !isHistorical && <Button size="sm" variant="outline" onClick={onEdit}>{labels(($) => $.artifacts_library.edit)}</Button>}
      </div>}

      <div className="min-h-0 flex-1 overflow-auto p-4">
        {isLoading && <PaneNotice>{t(($) => $.artifact.loading)}</PaneNotice>}
        {unavailable && (
          <PaneNotice>
            {t(($) => $.artifact.unavailable)}
            {/* A retry only makes sense for a failed fetch. The other arm of
                `unavailable` — a 200 that fell back to the empty artifact — is
                the server's answer, and refetching would return it again. */}
            {isError && (
              <>
                {" "}
                <button type="button" className="cursor-pointer underline" onClick={() => void refetch()}>
                  {t(($) => $.artifact.retry)}
                </button>
              </>
            )}
          </PaneNotice>
        )}
        {isHistorical && !shown && (
          <PaneNotice>
            {revisionQuery.isPending
              ? t(($) => $.artifact.loading)
              : t(($) => $.artifact.revision_unavailable)}
          </PaneNotice>
        )}
        {shown && (view === "preview" ? <ArtifactBody artifact={shown} /> : <pre className="whitespace-pre-wrap break-words rounded-lg bg-muted/30 p-4 font-mono text-xs"><code>{shown.content}</code></pre>)}
      </div>
    </aside>
  );
}

/** Quiet "the agent is writing to this" hint — no spinner, no layout shift. */
function UpdatingBadge() {
  const { t } = useT("assistant");
  return (
    <span
      role="status"
      className="shrink-0 animate-pulse rounded-full bg-brand/10 px-2 py-0.5 text-[11px] font-medium text-brand"
    >
      {t(($) => $.artifact.updating)}
    </span>
  );
}

/** Header title as a picker over every artifact produced in the session. */
function ArtifactSwitcher({
  sessionId,
  openArtifactId,
  title,
  onSelect,
}: {
  sessionId: string;
  openArtifactId: string;
  title: string;
  onSelect: (artifactId: string) => void;
}) {
  const { t } = useT("assistant");
  const kindLabel = useArtifactKindLabel();
  const timeAgo = useTimeAgo();
  const { data } = useQuery({
    ...assistantSessionArtifactListOptions(sessionId),
    enabled: !!sessionId,
  });
  const artifacts = (data ?? []).filter((entry) => entry.id);

  // One artifact is not a choice — and a list that failed to load must not
  // turn the title into a button that opens nothing.
  if (artifacts.length < 2) {
    return <span className="truncate text-sm font-medium">{title}</span>;
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={t(($) => $.artifact.switcher)}
        className="flex min-w-0 cursor-pointer items-center gap-1 rounded-md text-left text-sm font-medium transition-colors hover:text-brand data-popup-open:text-brand"
      >
        <span className="truncate">{title}</span>
        <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="bottom" sideOffset={6} className="min-w-64 max-w-80">
        {artifacts.map((entry) => {
          const EntryIcon = artifactKindIcon(entry.kind);
          return (
            <DropdownMenuItem key={entry.id} onClick={() => onSelect(entry.id)}>
              <Check
                className={cn(
                  "size-3.5 shrink-0",
                  entry.id !== openArtifactId && "opacity-0",
                )}
              />
              <EntryIcon className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">
                {entry.title.trim() || t(($) => $.artifact.untitled)}
              </span>
              <span className="shrink-0 text-xs text-muted-foreground">
                {kindLabel(entry.kind)} · {timeAgo(entry.updated_at)}
              </span>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** The version badge, promoted to a picker once there is history to pick. */
function VersionPicker({
  revisions,
  shownVersion,
  latestVersion,
  onSelect,
}: {
  revisions: AssistantArtifactRevisionSummary[];
  shownVersion: number;
  latestVersion: number;
  onSelect: (version: number) => void;
}) {
  const { t } = useT("assistant");
  const timeAgo = useTimeAgo();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={t(($) => $.artifact.versions)}
        className="flex shrink-0 cursor-pointer items-center gap-0.5 rounded-md tabular-nums transition-colors hover:text-foreground data-popup-open:text-foreground"
      >
        {t(($) => $.artifact.version_badge, { version: shownVersion })}
        <ChevronDown className="size-3" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="bottom" sideOffset={6} className="min-w-52">
        {revisions.map((entry) => (
          <DropdownMenuItem key={entry.id || entry.version} onClick={() => onSelect(entry.version)}>
            <Check
              className={cn("size-3.5 shrink-0", entry.version !== shownVersion && "opacity-0")}
            />
            <span className="flex-1 tabular-nums">
              {t(($) => $.artifact.version_badge, { version: entry.version })}
            </span>
            <span className="shrink-0 text-xs text-muted-foreground">
              {entry.version === latestVersion
                ? t(($) => $.artifact.version_latest)
                : timeAgo(entry.created_at)}
            </span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/**
 * Client-side export — the bytes are already in the Query cache, so a download
 * is a Blob and an anchor, never a second round trip. What each kind turns
 * into lives in lib/artifact-export.ts (and is tested there).
 */
function downloadArtifact(artifact: AssistantArtifact) {
  const { filename, mimeType, body } = artifactExport(artifact);
  const url = URL.createObjectURL(new Blob([body], { type: `${mimeType};charset=utf-8` }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}


function PaneNotice({ children }: { children: React.ReactNode }) {
  return <p className="py-8 text-center text-sm text-muted-foreground">{children}</p>;
}

function CopyContentButton({ content }: { content: string }) {
  const { t } = useT("assistant");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(timer);
  }, [copied]);

  return (
    <PaneAction
      label={copied ? t(($) => $.artifact.copied) : t(($) => $.artifact.copy)}
      onClick={() => {
        void copyText(content).then((ok) => {
          if (ok) setCopied(true);
        });
      }}
    >
      {copied ? <Check /> : <Copy />}
    </PaneAction>
  );
}

function PaneAction({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={label}
            className="shrink-0 text-muted-foreground"
            onClick={onClick}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent side="bottom">{label}</TooltipContent>
    </Tooltip>
  );
}
