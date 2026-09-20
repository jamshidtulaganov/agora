"use client";

import { cn } from "@agora/ui/lib/utils";
import type { ArtifactDiffLine } from "./artifact-diff";

/** One rendered line of a unified diff: old line no., new line no., marker,
 *  content.
 *
 *  It lives in its own module because two surfaces render diffs — the artifact
 *  code viewer (the agent's working copy) and the in-app Changes view (what
 *  GitHub says the pull request touches). A second renderer would be a second
 *  set of +/- tones to keep in step, and tone is the whole design here: the
 *  additions and deletions are washes over the row, not shouted colour.
 */
export function DiffLine({ line }: { line: ArtifactDiffLine }) {
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
