"use client";

import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { Download, Printer } from "lucide-react";
import type { ReactNode } from "react";
import { assistantArtifactOptions } from "@agora/core/assistant";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import type { ArtifactToolRef } from "../lib/artifact";
import { toArtifactKind } from "../lib/artifact";
import { artifactExport } from "../lib/artifact-export";
import {
  buildPrintDocument,
  printInSandbox,
  renderedChartSvg,
  serializeRenderedBody,
} from "../lib/artifact-print";
import { ArtifactBody } from "./artifact-body";
import { artifactKindIcon } from "./artifact-kind";

/**
 * A chart, table, document or interactive page the Assistant made, shown
 * right in the conversation — with Print (the browser dialog also saves as
 * PDF) and Download in its header. There is no separate artifact pane or
 * library: the chat is where an artifact lives.
 *
 * Only the LAST transcript row for an artifact renders it in full; earlier
 * create/update rows collapse to a one-line note (see ArtifactHistoryRow), so
 * an artifact that was revised twice appears once, at its latest version.
 */
export function InlineArtifact({ artifact: ref }: { artifact: ArtifactToolRef }) {
  const { t } = useT("assistant");
  const bodyRef = useRef<HTMLDivElement>(null);
  const { data, isLoading, isError, refetch } = useQuery(assistantArtifactOptions(ref.artifactId));
  // `id === ""` is the schema fallback for a malformed 200 — same as a 404.
  const artifact = data && data.id ? data : null;
  const unavailable = !isLoading && (isError || !artifact);
  const title = (artifact?.title || ref.title).trim() || t(($) => $.artifact.untitled);
  const Icon = artifactKindIcon(artifact?.kind ?? ref.kind);
  const version = artifact?.version ?? ref.version;
  const isHtml = toArtifactKind(artifact?.kind ?? ref.kind) === "html";

  const handlePrint = () => {
    if (!artifact) return;
    const body = bodyRef.current ? serializeRenderedBody(bodyRef.current) : "";
    printInSandbox(buildPrintDocument({ ...artifact, title }, body));
  };

  const handleDownload = () => {
    if (!artifact) return;
    const chartSvg = bodyRef.current ? renderedChartSvg(bodyRef.current) : null;
    downloadFile(artifactExport({ ...artifact, title }, { chartSvg }));
  };

  return (
    <figure className="ml-8 min-w-0 max-w-3xl overflow-hidden rounded-lg border border-border bg-card">
      <figcaption className="flex items-center gap-2 border-b border-border py-1 pl-3 pr-1">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{title}</span>
        {version > 1 && (
          <Badge variant="secondary" className="shrink-0 tabular-nums">
            {t(($) => $.artifact.version_badge, { version })}
          </Badge>
        )}
        <HeaderAction label={t(($) => $.artifact.print)} onClick={handlePrint} disabled={!artifact}>
          <Printer />
        </HeaderAction>
        <HeaderAction label={t(($) => $.artifact.download)} onClick={handleDownload} disabled={!artifact}>
          <Download />
        </HeaderAction>
      </figcaption>
      <div ref={bodyRef} className={cn("p-3", !isHtml && "max-h-[560px] overflow-auto")}>
        {isLoading ? (
          <div role="status" aria-label={t(($) => $.artifact.loading)} className="h-40 animate-pulse rounded-md bg-muted/60" />
        ) : unavailable ? (
          <div className="flex items-center justify-between gap-3 py-2 text-sm text-muted-foreground">
            <span>{t(($) => $.artifact.unavailable)}</span>
            <Button variant="outline" size="sm" onClick={() => void refetch()}>
              {t(($) => $.artifact.retry)}
            </Button>
          </div>
        ) : artifact ? (
          <ArtifactBody artifact={artifact} frameClassName="h-[440px]" />
        ) : null}
      </div>
    </figure>
  );
}

/**
 * An earlier create/update of an artifact that is shown in full further down
 * the conversation: one quiet line, so the transcript still reads in order
 * without repeating the chart.
 */
export function ArtifactHistoryRow({ artifact, updated }: { artifact: ArtifactToolRef; updated: boolean }) {
  const { t } = useT("assistant");
  const Icon = artifactKindIcon(artifact.kind);
  const title = artifact.title.trim() || t(($) => $.artifact.untitled);
  return (
    <div className="ml-8 flex min-w-0 items-center gap-1.5 px-2 py-0.5 text-xs text-muted-foreground">
      <Icon className="size-3 shrink-0" />
      <span className="truncate">
        {updated
          ? t(($) => $.tool_chip.verb.update, { object: title })
          : t(($) => $.tool_chip.verb.create, { object: title })}
      </span>
    </div>
  );
}

function HeaderAction({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={label}
            disabled={disabled}
            onClick={onClick}
            className="text-muted-foreground hover:text-foreground"
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

/**
 * Client-side export — the bytes are already in the Query cache (or on
 * screen, for a chart picture), so a download is a Blob and an anchor, never
 * a second round trip. What each kind turns into lives in
 * lib/artifact-export.ts.
 */
function downloadFile({ filename, mimeType, body }: ReturnType<typeof artifactExport>) {
  const url = URL.createObjectURL(new Blob([body], { type: `${mimeType};charset=utf-8` }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

