/* eslint-disable i18next/no-literal-string -- co-code editor surface; i18n follow-up */
"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  GitBranch,
  GitPullRequest,
  Check,
  X,
  Loader2,
  ExternalLink,
  ShieldCheck,
  Copy,
  ChevronDown,
  SquareArrowOutUpRight,
} from "lucide-react";
import {
  issuePullRequestsOptions,
  githubKeys,
  derivePullRequestStatusKind,
} from "@agora/core/github";
import { cn } from "@agora/ui/lib/utils";
import { proxyHeaders, absoluteBase } from "./editor-proxy-fetch";
import { EditorGates } from "./editor-gates";

// The co-code "trust bar". Built for a traditional developer who reviews every
// change before it merges: it shows that the agent's work is isolated on a
// branch (their main is untouched), surfaces the PR's CI status once a PR
// exists (Verify), and gives two plain controls —
//   Accept  → commit + push + open (or update) a pull request, which also
//             triggers CI and routes the work into the normal GitHub review/merge
//             flow the dev already trusts;
//   Discard → reset the worktree to its base (local only — pushed commits stay
//             recoverable on the remote).
// Self-host only: it talks to the daemon directly (browser → 127.0.0.1), the
// same path as /editor/launch and /editor/changes.

interface RepoChange {
  repo: string;
  branch: string;
  base: string;
  files: { path: string; additions: number; deletions: number }[];
}

interface PrResult {
  repo: string;
  branch: string;
  url?: string;
  created: boolean;
  skipped?: string;
  error?: string;
}

type CiIcon = "check" | "x" | "spin" | "dot";

function ciIcon(kind: CiIcon) {
  switch (kind) {
    case "check":
      return <Check className="h-3 w-3" />;
    case "x":
      return <X className="h-3 w-3" />;
    case "spin":
      return <Loader2 className="h-3 w-3 animate-spin" />;
    default:
      return <span className="inline-block h-1.5 w-1.5 rounded-full bg-current" />;
  }
}

export function EditorReviewBar({
  issueId,
  issueKey,
  issueTitle,
  daemonUrl,
  workdir,
}: {
  issueId: string;
  issueKey?: string;
  issueTitle?: string;
  daemonUrl: string;
  workdir: string;
}) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState<"accept" | "discard" | null>(null);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [showLocal, setShowLocal] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);
  const [msg, setMsg] = useState<{
    kind: "ok" | "err";
    text: string;
    url?: string;
  } | null>(null);

  const copy = (key: string, text: string) => {
    void navigator.clipboard?.writeText(text);
    setCopied(key);
    window.setTimeout(() => setCopied((c) => (c === key ? null : c)), 1500);
  };

  // Changes summary — reuses the daemon /editor/changes endpoint. Polled so the
  // branch + file count stay fresh while the agent edits.
  const { data: changes } = useQuery({
    queryKey: ["editor-changes", workdir],
    queryFn: async () => {
      const r = await fetch(`${absoluteBase(daemonUrl)}/editor/changes`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        body: JSON.stringify({ workdir }),
      });
      if (!r.ok) throw new Error("changes lookup failed");
      return (await r.json()) as { repos: RepoChange[] };
    },
    enabled: !!daemonUrl && !!workdir,
    refetchInterval: 8000,
  });

  // PR status (Verify) — reuses the existing per-issue PR list + server-computed
  // CI status. Polled so a freshly-opened PR's checks update without a refresh.
  const { data: prData } = useQuery({
    ...issuePullRequestsOptions(issueId),
    refetchInterval: 15000,
  });
  const prs = prData?.pull_requests ?? [];
  const openPr =
    prs.find((p) => p.state === "open" || p.state === "draft") ?? prs[0];

  const repos = changes?.repos ?? [];
  const primary = repos.find((r) => r.files.length > 0) ?? repos[0];
  const totalFiles = repos.reduce((n, r) => n + r.files.length, 0);
  const hasChanges = totalFiles > 0;

  // "Stay in my tools" — the worktree is a real git checkout on this machine.
  const repoPath = primary ? `${workdir}/${primary.repo}` : workdir;
  const vscodeUri = `vscode://file${repoPath}`;
  const checkoutCmd = primary?.branch
    ? `git fetch origin ${primary.branch} && git switch ${primary.branch}`
    : "";

  const accept = async () => {
    setBusy("accept");
    setMsg(null);
    try {
      const title = issueKey
        ? `${issueKey}: ${issueTitle ?? ""}`.trim()
        : (issueTitle ?? "");
      const body = issueKey
        ? `Co-coded with the agent in Agora.\n\nCloses ${issueKey}`
        : "Co-coded with the agent in Agora.";
      const r = await fetch(`${absoluteBase(daemonUrl)}/editor/open-pr`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        body: JSON.stringify({ workdir, title, body }),
      });
      if (!r.ok) throw new Error(`open PR failed (${r.status})`);
      const { results } = (await r.json()) as { results: PrResult[] };
      const made = results.find((x) => x.url);
      const errd = results.find((x) => x.error);
      if (made?.url) {
        setMsg({
          kind: "ok",
          text: made.created ? "Pull request opened" : "Pull request updated",
          url: made.url,
        });
      } else if (errd?.error) {
        setMsg({ kind: "err", text: errd.error });
      } else {
        const sk = results.find((x) => x.skipped);
        setMsg({
          kind: "err",
          text: sk?.skipped ? `Nothing to open — ${sk.skipped}` : "Nothing to open",
        });
      }
      qc.invalidateQueries({ queryKey: githubKeys.pullRequests(issueId) });
    } catch (e) {
      setMsg({ kind: "err", text: e instanceof Error ? e.message : "open PR failed" });
    } finally {
      setBusy(null);
    }
  };

  const discard = async () => {
    setBusy("discard");
    setMsg(null);
    setConfirmDiscard(false);
    try {
      const r = await fetch(`${absoluteBase(daemonUrl)}/editor/discard`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        body: JSON.stringify({ workdir }),
      });
      if (!r.ok) throw new Error(`discard failed (${r.status})`);
      setMsg({ kind: "ok", text: "Changes discarded — worktree reset to base" });
      qc.invalidateQueries({ queryKey: ["editor-changes", workdir] });
    } catch (e) {
      setMsg({ kind: "err", text: e instanceof Error ? e.message : "discard failed" });
    } finally {
      setBusy(null);
    }
  };

  // CI status pill (Verify) — derived from the same priority table the PR
  // sidebar uses, mapped to a compact label + icon.
  const ci = (() => {
    if (!openPr) return null;
    const kind = derivePullRequestStatusKind({
      state: openPr.state,
      mergeable_state: openPr.mergeable_state,
      checks_failed: openPr.checks_failed,
      checks_pending: openPr.checks_pending,
      checks_passed: openPr.checks_passed,
    });
    const map: Record<string, { text: string; cls: string; icon: CiIcon }> = {
      checks_passed: { text: "checks passed", cls: "text-emerald-600 dark:text-emerald-400", icon: "check" },
      checks_failed: { text: "checks failed", cls: "text-destructive", icon: "x" },
      checks_pending: { text: "checks running", cls: "text-amber-600 dark:text-amber-400", icon: "spin" },
      ready: { text: "ready to merge", cls: "text-emerald-600 dark:text-emerald-400", icon: "check" },
      conflicts: { text: "merge conflicts", cls: "text-destructive", icon: "x" },
      merged: { text: "merged", cls: "text-violet-600 dark:text-violet-400", icon: "check" },
      closed: { text: "closed", cls: "text-muted-foreground", icon: "x" },
      unknown: { text: "no checks yet", cls: "text-muted-foreground", icon: "dot" },
    };
    return map[kind] ?? map.unknown;
  })();

  return (
    <div className="shrink-0 space-y-1.5 border-b border-border bg-muted/20 px-3 py-2 text-[11px]">
      {/* One plain status line: what changed + the safety reassurance. The raw
          branch name + base SHA are git jargon a non-engineer never needs — they
          move into Developer options below. */}
      <div className="flex items-center gap-1.5 text-muted-foreground">
        <GitBranch className="h-3 w-3 shrink-0" />
        <span className="text-foreground">
          {hasChanges
            ? `${totalFiles} file${totalFiles === 1 ? "" : "s"} changed`
            : "No changes yet"}
        </span>
        <span className="ml-auto inline-flex shrink-0 items-center gap-1 text-emerald-600 dark:text-emerald-500">
          <ShieldCheck className="h-3 w-3 shrink-0" />
          Won&apos;t touch main
        </span>
      </div>

      {/* Developer options — the raw branch/SHA + a real local git checkout, so
          a dev who prefers their own IDE isn't trapped in the browser. Collapsed
          by default so the bar reads as a plain "N changed · safe · ship it". */}
      <div>
        <button
          type="button"
          onClick={() => setShowLocal((s) => !s)}
          className="flex w-full items-center gap-1 text-[10px] text-muted-foreground transition-colors hover:text-foreground"
        >
          <ChevronDown
            className={cn(
              "h-3 w-3 shrink-0 transition-transform",
              showLocal ? "" : "-rotate-90",
            )}
          />
          Developer options
        </button>
        {showLocal && (
          <div className="mt-1 space-y-1 pl-1">
            {primary?.branch && (
              <div className="flex items-center gap-1 font-mono text-[10px] text-muted-foreground">
                <span className="min-w-0 truncate" title={primary.branch}>
                  {primary.branch}
                </span>
                {primary.base && (
                  <span className="shrink-0">→ {primary.base.slice(0, 7)}</span>
                )}
              </div>
            )}
            <a
              href={vscodeUri}
              className="inline-flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-[10px] transition-colors hover:bg-accent"
            >
              <SquareArrowOutUpRight className="h-2.5 w-2.5" />
              Open this folder in VS Code
            </a>
            {checkoutCmd && (
              <div className="flex items-center gap-1">
                <code
                  className="min-w-0 flex-1 truncate rounded bg-muted px-1 py-0.5 font-mono text-[10px]"
                  title={checkoutCmd}
                >
                  {checkoutCmd}
                </code>
                <button
                  type="button"
                  onClick={() => copy("cmd", checkoutCmd)}
                  title="Copy: pull this branch into your own clone"
                  className="shrink-0 rounded border border-border p-0.5 transition-colors hover:bg-accent"
                >
                  {copied === "cmd" ? (
                    <Check className="h-2.5 w-2.5 text-emerald-500" />
                  ) : (
                    <Copy className="h-2.5 w-2.5" />
                  )}
                </button>
              </div>
            )}
            <div className="flex items-center gap-1">
              <code
                className="min-w-0 flex-1 truncate rounded bg-muted px-1 py-0.5 font-mono text-[10px]"
                title={repoPath}
              >
                {repoPath}
              </code>
              <button
                type="button"
                onClick={() => copy("path", repoPath)}
                title="Copy the local worktree path"
                className="shrink-0 rounded border border-border p-0.5 transition-colors hover:bg-accent"
              >
                {copied === "path" ? (
                  <Check className="h-2.5 w-2.5 text-emerald-500" />
                ) : (
                  <Copy className="h-2.5 w-2.5" />
                )}
              </button>
            </div>
          </div>
        )}
      </div>

      {/* PR + CI status (Verify). */}
      {openPr && ci && (
        <div className="flex items-center gap-1.5">
          <GitPullRequest className="h-3 w-3 shrink-0 text-muted-foreground" />
          <a
            href={openPr.html_url}
            target="_blank"
            rel="noreferrer noopener"
            className="text-foreground hover:underline"
          >
            #{openPr.number}
          </a>
          <span className="text-muted-foreground">·</span>
          <a
            href={openPr.html_url}
            target="_blank"
            rel="noreferrer noopener"
            className={cn("inline-flex items-center gap-1", ci.cls)}
          >
            {ciIcon(ci.icon)}
            {ci.text}
          </a>
        </div>
      )}

      {/* Merge gates (deterministic: ci/qa/security/code-review). */}
      <EditorGates issueId={issueId} />

      {/* Accept / Discard controls. */}
      <div className="flex items-center gap-1.5 pt-0.5">
        <button
          type="button"
          disabled={busy !== null || !hasChanges}
          onClick={() => void accept()}
          title={
            openPr
              ? "Commit, push, and update the pull request"
              : "Commit, push, and open a pull request (runs CI)"
          }
          className="inline-flex flex-1 items-center justify-center gap-1 rounded-md bg-primary px-2 py-1 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
        >
          {busy === "accept" ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <GitPullRequest className="h-3 w-3" />
          )}
          {openPr ? "Accept → Update PR" : "Accept → Open PR"}
        </button>
        {confirmDiscard ? (
          <>
            <button
              type="button"
              disabled={busy !== null}
              onClick={() => void discard()}
              className="inline-flex items-center gap-1 rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground transition-colors hover:bg-destructive/90 disabled:opacity-50"
            >
              {busy === "discard" && <Loader2 className="h-3 w-3 animate-spin" />}
              Confirm discard
            </button>
            <button
              type="button"
              onClick={() => setConfirmDiscard(false)}
              className="rounded-md border border-border px-2 py-1 text-xs transition-colors hover:bg-accent"
            >
              Cancel
            </button>
          </>
        ) : (
          <button
            type="button"
            disabled={busy !== null || !hasChanges}
            onClick={() => setConfirmDiscard(true)}
            title="Discard the changes and reset the worktree to its base"
            className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
          >
            <X className="h-3 w-3" />
            Discard
          </button>
        )}
      </div>

      {msg && (
        <div
          className={cn(
            "flex flex-wrap items-center gap-1 pt-0.5",
            msg.kind === "ok"
              ? "text-emerald-600 dark:text-emerald-400"
              : "text-destructive",
          )}
        >
          {msg.kind === "ok" ? (
            <Check className="h-3 w-3 shrink-0" />
          ) : (
            <X className="h-3 w-3 shrink-0" />
          )}
          <span className="min-w-0 break-words">{msg.text}</span>
          {msg.url && (
            <a
              href={msg.url}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-0.5 underline hover:no-underline"
            >
              view <ExternalLink className="h-2.5 w-2.5" />
            </a>
          )}
        </div>
      )}
    </div>
  );
}
