"use client";

import { Markdown } from "../../common/markdown";
import { CodeBlockIframe } from "../../editor/code-block-iframe";
import { useT } from "../../i18n";
import { parseChartSpec, parseTableSpec, toArtifactKind } from "../lib/artifact";
import type { TableCell } from "../lib/artifact";
import { ArtifactChart } from "./artifact-chart";

/**
 * The minimum an artifact renderer needs. Kept structural rather than tied to
 * `AssistantArtifact` so the same renderers serve BOTH surfaces that show an
 * artifact body: the owner's editable pane, and the read-only report viewer on
 * the project page (which reads a pin, not an artifact). One renderer, one
 * security posture, one set of downgrade paths — see the No-Duplication rule.
 */
export interface ArtifactRenderable {
  /** May be an unknown future kind — the body downgrades to raw content. */
  kind: string;
  content: string;
  title: string;
}

export function ArtifactBody({ artifact }: { artifact: ArtifactRenderable }) {
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
