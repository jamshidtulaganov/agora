import { queryOptions } from "@tanstack/react-query";
import { api } from "../api";

export const githubKeys = {
  all: (wsId: string) => ["github", wsId] as const,
  installations: (wsId: string) => [...githubKeys.all(wsId), "installations"] as const,
  pullRequests: (issueId: string) => ["github", "pull-requests", issueId] as const,
  /** Files changed per linked PR. Kept under its own second segment so the
   *  `pull_request` realtime handler can invalidate both lists by prefix. */
  issueChanges: (issueId: string) => ["github", "issue-changes", issueId] as const,
  issueChangePatch: (issueId: string, prNumber: number, path: string) =>
    ["github", "issue-change-patch", issueId, prNumber, path] as const,
};

export const githubInstallationsOptions = (wsId: string) =>
  queryOptions({
    queryKey: githubKeys.installations(wsId),
    queryFn: () => api.listGitHubInstallations(wsId),
    enabled: !!wsId,
  });

export const issuePullRequestsOptions = (issueId: string) =>
  queryOptions({
    queryKey: githubKeys.pullRequests(issueId),
    queryFn: () => api.listIssuePullRequests(issueId),
    enabled: !!issueId,
  });

/** The in-app Changes view: which files each linked pull request touches.
 *  Returns `{ changes: [] }` for an issue with no PR — the section renders
 *  nothing at all rather than an empty card. */
export const issueChangesOptions = (issueId: string) =>
  queryOptions({
    queryKey: githubKeys.issueChanges(issueId),
    queryFn: () => api.getIssueChanges(issueId),
    enabled: !!issueId,
  });

/** One file's unified diff, fetched only when the row is expanded.
 *  `staleTime: Infinity` because a patch is pinned to a commit: it changes
 *  when the PR does, and that arrives as a realtime invalidation, not as a
 *  refetch timer. */
export const issueChangePatchOptions = (
  issueId: string,
  prNumber: number,
  path: string,
  enabled: boolean,
) =>
  queryOptions({
    queryKey: githubKeys.issueChangePatch(issueId, prNumber, path),
    queryFn: () => api.getIssueChangePatch(issueId, prNumber, path),
    enabled: enabled && !!issueId && !!path,
    staleTime: Number.POSITIVE_INFINITY,
  });
