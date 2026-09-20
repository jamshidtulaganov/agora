"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ChevronRight,
  ExternalLink,
  FileCode2,
  FileDiff,
  FileMinus,
  FilePlus,
  FileSymlink,
  GitPullRequest,
  Loader2,
} from "lucide-react";
import {
  issueChangePatchOptions,
  issueChangesOptions,
} from "@agora/core/github";
import type { IssueChange, IssueChangeFile } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { useT } from "../../i18n";
import { parseArtifactFileDiff } from "./artifact-diff";
import { DiffLine } from "./diff-line";
import { STATE_ICON, getStateLabel } from "./pull-request-list";

// WHAT THE AGENT CHANGED, IN AGORA.
//
// Until this section existed, the honest answer to "what did the agent do" was
// a link to GitHub. That link is still here — it belongs next to the work, and
// GitHub is better at review threads than we will ever be — but it is no longer
// the ONLY way to see the diff. The first question a reviewer asks is "which
// files moved", and that answer now renders in place.
//
// THREE RULES THIS SECTION KEEPS:
//
//  1. It renders NOTHING when there is nothing to say. No PR linked means no
//     section — not an empty card explaining its own emptiness.
//  2. It never claims more than it knows. When the file list came from stored
//     paths (files_source = "stored") the +/- counts are ABSENT, not zero: a
//     row reading "+0 −0" is a confident lie about a file that changed.
//  3. Patches load one file at a time, on click. A PR touching 200 files must
//     not pull 200 diffs into the page to answer "which files".

const MAX_DIFF_LINES = 1_200;

/** Path labels get long and the interesting half is the END. Trimming the
 *  middle keeps both the top-level directory and the filename readable, which
 *  a CSS `truncate` (tail-only) does not. */
function middleTruncate(path: string, max = 72): string {
  if (path.length <= max) return path;
  const head = path.slice(0, 18);
  const tail = path.slice(path.length - (max - head.length - 1));
  return `${head}…${tail}`;
}

/** Status is server-driven and GitHub can add a verb at any time, so the map
 *  ends in a neutral glyph rather than in undefined. */
function statusGlyph(status: string) {
  switch (status) {
    case "added":
      return FilePlus;
    case "removed":
      return FileMinus;
    case "renamed":
    case "copied":
      return FileSymlink;
    case "modified":
    case "changed":
      return FileDiff;
    default:
      return FileCode2;
  }
}

export function ChangesSection({
  issueId,
  className,
}: {
  issueId: string;
  className?: string;
}) {
  const { t } = useT("issues");
  const { data } = useQuery(issueChangesOptions(issueId));
  const changes = data?.changes ?? [];

  // No PRs (or still loading) → nothing. A placeholder here would occupy the
  // reading flow of every issue that never produced code, which is most of them.
  if (changes.length === 0) return null;

  return (
    <section className={className} data-testid="changes-section">
      <div className="mb-2 text-[11px] uppercase tracking-wide text-muted-foreground">
        {t(($) => $.changes.heading)}
      </div>
      <div className="space-y-3">
        {changes.map((change) => (
          <PullRequestChanges
            key={`${change.repo_owner}/${change.repo_name}#${change.pr_number}`}
            issueId={issueId}
            change={change}
          />
        ))}
      </div>
    </section>
  );
}

function PullRequestChanges({
  issueId,
  change,
}: {
  issueId: string;
  change: IssueChange;
}) {
  const { t } = useT("issues");
  const cfg = STATE_ICON[change.state] ?? { icon: GitPullRequest, className: "" };
  const StateIcon = cfg.icon;
  // Stats belong to the PR row itself (they arrive on the webhook payload), so
  // they are shown whatever the file list's source is — unlike per-file counts.
  const showStats = change.additions > 0 || change.deletions > 0;

  return (
    <div className="overflow-hidden rounded-lg border" data-testid="changes-pr">
      <div className="flex items-center gap-2 border-b bg-muted/20 px-3 py-2">
        <StateIcon className={cn("size-3.5 shrink-0", cfg.className)} aria-hidden />
        <span className="min-w-0 truncate text-xs font-medium">
          {change.repo_owner}/{change.repo_name}#{change.pr_number}
        </span>
        <span className="shrink-0 text-[11px] text-muted-foreground">
          {getStateLabel(change.state, t)}
        </span>
        {showStats && (
          <span className="shrink-0 font-mono text-[11px] tabular-nums">
            <span className="text-success">+{change.additions}</span>{" "}
            <span className="text-destructive">−{change.deletions}</span>
          </span>
        )}
        {change.files.length > 0 && (
          <span className="shrink-0 text-[11px] text-muted-foreground">
            {t(($) => $.artifact.file_count, { count: change.files.length })}
          </span>
        )}
        <a
          href={change.html_url}
          target="_blank"
          rel="noreferrer noopener"
          className="ml-auto inline-flex shrink-0 items-center gap-1 text-[11px] font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          {t(($) => $.changes.view_on_github)}
          <ExternalLink className="size-3" aria-hidden />
        </a>
      </div>

      {change.files.length === 0 ? (
        <p className="px-3 py-3 text-[11px] text-muted-foreground">
          {t(($) => $.changes.files_unavailable)}
        </p>
      ) : (
        <ul className="divide-y">
          {change.files.map((file) => (
            <ChangedFileRow
              key={file.path}
              issueId={issueId}
              prNumber={change.pr_number}
              file={file}
              showCounts={change.files_source === "github"}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function ChangedFileRow({
  issueId,
  prNumber,
  file,
  showCounts,
}: {
  issueId: string;
  prNumber: number;
  file: IssueChangeFile;
  showCounts: boolean;
}) {
  const { t } = useT("issues");
  const [open, setOpen] = useState(false);
  const Glyph = statusGlyph(file.status);

  return (
    <li>
      <button
        type="button"
        data-testid="changed-file"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs outline-none transition-colors hover:bg-accent/40 focus-visible:bg-accent/40"
      >
        <ChevronRight
          className={cn("size-3 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
          aria-hidden
        />
        <Glyph className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
        <span className="min-w-0 flex-1 font-mono text-[11px]" title={file.path}>
          {middleTruncate(file.path)}
        </span>
        {showCounts && (file.additions > 0 || file.deletions > 0) && (
          <span className="shrink-0 font-mono text-[10px] tabular-nums">
            {file.additions > 0 && <span className="text-success">+{file.additions}</span>}
            {file.deletions > 0 && (
              <span className="ml-1 text-destructive">−{file.deletions}</span>
            )}
          </span>
        )}
      </button>
      {file.previous_path && (
        <p className="px-3 pb-1.5 pl-11 text-[10px] text-muted-foreground">
          {t(($) => $.changes.renamed_from, { path: file.previous_path })}
        </p>
      )}
      {open && <FileDiffPane issueId={issueId} prNumber={prNumber} path={file.path} />}
    </li>
  );
}

function FileDiffPane({
  issueId,
  prNumber,
  path,
}: {
  issueId: string;
  prNumber: number;
  path: string;
}) {
  const { t } = useT("issues");
  const { data, isLoading, isError } = useQuery(
    issueChangePatchOptions(issueId, prNumber, path, true),
  );
  const patch = data?.patch ?? null;
  const lines = useMemo(
    () => (patch ? parseArtifactFileDiff(patch, path) : []),
    [patch, path],
  );

  if (isLoading) {
    return (
      <p className="flex items-center gap-2 px-3 py-3 pl-11 text-[11px] text-muted-foreground">
        <Loader2 className="size-3 animate-spin motion-reduce:animate-none" aria-hidden />
        {t(($) => $.changes.loading_diff)}
      </p>
    );
  }

  if (isError || !patch || lines.length === 0) {
    return (
      <p
        data-testid="changed-file-no-diff"
        className="px-3 py-3 pl-11 text-[11px] text-muted-foreground"
      >
        {diffUnavailableText(isError ? "fetch_failed" : data?.reason ?? "", t)}
      </p>
    );
  }

  return (
    <div
      data-testid="changed-file-diff"
      className="max-h-96 overflow-auto border-t bg-white font-mono text-[11px] leading-5 text-[#1f2328] dark:bg-[#282c34] dark:text-[#abb2bf]"
      translate="no"
    >
      {lines.slice(0, MAX_DIFF_LINES).map((line, index) => (
        <DiffLine key={index} line={line} />
      ))}
      {lines.length > MAX_DIFF_LINES && (
        <p className="border-t bg-muted/40 px-3 py-2 font-sans text-muted-foreground">
          {t(($) => $.changes.diff_truncated)}
        </p>
      )}
    </div>
  );
}

/** Every "no diff" state gets its own sentence. The `default` branch is
 *  load-bearing: the server can add a reason, and an unrecognised one must
 *  still read as an explanation rather than as a blank pane. */
function diffUnavailableText(
  reason: string,
  t: ReturnType<typeof useT<"issues">>["t"],
): string {
  switch (reason) {
    case "github_app_not_configured":
      return t(($) => $.changes.diff_not_configured);
    case "file_not_found":
      return t(($) => $.changes.diff_file_missing);
    case "patch_unavailable":
      return t(($) => $.changes.diff_too_large);
    default:
      return t(($) => $.changes.diff_failed);
  }
}
