"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Copy, X } from "lucide-react";
import { assistantArtifactOptions } from "@agora/core/assistant";
import type { AssistantArtifact } from "@agora/core/types";
import { Button } from "@agora/ui/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { copyText } from "@agora/ui/lib/clipboard";
import { Markdown } from "../../common/markdown";
import { CodeBlockIframe } from "../../editor/code-block-iframe";
import { useT } from "../../i18n";
import { parseChartSpec, parseTableSpec, toArtifactKind } from "../lib/artifact";
import type { TableCell } from "../lib/artifact";
import { ArtifactChart } from "./artifact-chart";
import { artifactKindIcon, useArtifactKindLabel } from "./artifact-card";

interface ArtifactPaneProps {
  artifactId: string;
  onClose: () => void;
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
export function ArtifactPane({ artifactId, onClose }: ArtifactPaneProps) {
  const { t } = useT("assistant");
  const kindLabel = useArtifactKindLabel();
  const { data, isLoading, isError } = useQuery(assistantArtifactOptions(artifactId));

  // `id === ""` is the EMPTY_ASSISTANT_ARTIFACT fallback — a 200 whose body
  // failed schema validation. Same user-visible outcome as a 404.
  const artifact = data && data.id ? data : null;
  const unavailable = !isLoading && (isError || !artifact);
  const Icon = artifactKindIcon(artifact?.kind ?? "");

  return (
    <aside className="flex w-[40%] min-w-[360px] max-w-[760px] shrink-0 flex-col border-l bg-background">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon className="size-4 shrink-0 text-muted-foreground" />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-sm font-medium">
            {artifact?.title.trim() || t(($) => $.artifact.untitled)}
          </span>
          {artifact && (
            <span className="truncate text-xs text-muted-foreground">
              {t(($) => $.artifact.meta, {
                kind: kindLabel(artifact.kind),
                version: artifact.version,
              })}
            </span>
          )}
        </div>
        {artifact && <CopyContentButton content={artifact.content} />}
        <PaneAction label={t(($) => $.artifact.close)} onClick={onClose}>
          <X />
        </PaneAction>
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-4">
        {isLoading && <PaneNotice>{t(($) => $.artifact.loading)}</PaneNotice>}
        {unavailable && <PaneNotice>{t(($) => $.artifact.unavailable)}</PaneNotice>}
        {artifact && <ArtifactBody artifact={artifact} />}
      </div>
    </aside>
  );
}

function ArtifactBody({ artifact }: { artifact: AssistantArtifact }) {
  const kind = toArtifactKind(artifact.kind);

  switch (kind) {
    case "chart": {
      const spec = parseChartSpec(artifact.content);
      return spec ? <ArtifactChart spec={spec} /> : <RawContent content={artifact.content} />;
    }
    case "table": {
      const spec = parseTableSpec(artifact.content);
      return spec ? (
        <ArtifactTable columns={spec.columns} rows={spec.rows} />
      ) : (
        <RawContent content={artifact.content} />
      );
    }
    case "markdown":
      return (
        <div className="prose prose-sm dark:prose-invert max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
          <Markdown>{artifact.content}</Markdown>
        </div>
      );
    case "html":
      // Security invariant (plan §7.1): artifact HTML renders ONLY here, in
      // CodeBlockIframe's `sandbox="allow-scripts"` opaque-origin frame —
      // never inlined, never through the markdown pipeline, never with
      // allow-same-origin. Do not swap this for another renderer.
      return (
        <CodeBlockIframe
          html={artifact.content}
          title={artifact.title}
          heightClassName="h-full min-h-[480px]"
          // Model-generated HTML must not phone home: the sandbox blocks DOM
          // escape but not fetch(); this injects a no-network CSP (plan §7 +
          // final-plan amendment 2).
          restrictNetwork
        />
      );
    default:
      // An unknown kind from a newer server: show what we got instead of
      // guessing at a renderer (enum drift downgrades, never crashes).
      return <RawContent content={artifact.content} />;
  }
}

/** The downgrade view: a malformed spec or a kind this build can't render. */
function RawContent({ content }: { content: string }) {
  const { t } = useT("assistant");
  return (
    <div className="flex flex-col gap-2">
      <p className="text-xs text-muted-foreground">{t(($) => $.artifact.downgrade_notice)}</p>
      <pre className="overflow-auto rounded-md border border-border bg-muted/40 p-3 text-xs">
        <code>{content}</code>
      </pre>
    </div>
  );
}

function ArtifactTable({ columns, rows }: { columns: string[]; rows: TableCell[][] }) {
  return (
    <div className="overflow-x-auto rounded-md border border-border">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-border bg-muted/40">
            {columns.map((column, index) => (
              <th
                key={index}
                className="px-3 py-2 text-left text-xs font-medium text-muted-foreground"
              >
                {column}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => (
            <tr key={rowIndex} className="border-b border-border last:border-0">
              {columns.map((_column, cellIndex) => {
                const cell = row[cellIndex];
                return (
                  <td
                    key={cellIndex}
                    className={
                      typeof cell === "number"
                        ? "px-3 py-2 text-right tabular-nums"
                        : "px-3 py-2"
                    }
                  >
                    {cell === null || cell === undefined ? EM_DASH : String(cell)}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const EM_DASH = "—";

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
