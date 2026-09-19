import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import {
  isKnownStaleReason,
  staleIssuesOptions,
  stalenessKeys,
  STALENESS_STALE_TIME_MS,
  toStaleIssueMap,
} from "./staleness";
import { onIssueCreated, onIssueDeleted, onIssueUpdated } from "./ws-updaters";
import type { Issue, StaleIssue } from "../types";

const WS_ID = "ws-1";

function staleRow(over: Partial<StaleIssue> = {}): StaleIssue {
  return {
    issue_id: "issue-1",
    identifier: "MUL-123",
    title: "Wire the staleness endpoint",
    status: "in_review",
    reason: "review_done",
    since: "2026-09-16T00:00:00Z",
    ...over,
  };
}

const baseIssue: Issue = {
  id: "issue-1",
  workspace_id: WS_ID,
  number: 1,
  identifier: "MUL-1",
  title: "Test",
  description: null,
  status: "todo",
  priority: "none",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("isKnownStaleReason", () => {
  it("accepts the four rules this build has copy for", () => {
    expect(isKnownStaleReason("idle")).toBe(true);
    expect(isKnownStaleReason("review_done")).toBe(true);
    expect(isKnownStaleReason("reopened_work")).toBe(true);
    expect(isKnownStaleReason("blocked_quiet")).toBe(true);
  });

  it("rejects a reason a newer server invented, so it renders generically", () => {
    expect(isKnownStaleReason("date_slipped_in_slack")).toBe(false);
    expect(isKnownStaleReason("")).toBe(false);
  });
});

describe("toStaleIssueMap", () => {
  it("keys rows by issue id", () => {
    const a = staleRow();
    const b = staleRow({ issue_id: "issue-2", reason: "idle" });
    const map = toStaleIssueMap({ stale: [a, b] });
    expect(map.get("issue-1")).toEqual(a);
    expect(map.get("issue-2")).toEqual(b);
    expect(map.get("issue-3")).toBeUndefined();
  });

  it("drops a row that can't be matched to an issue", () => {
    const map = toStaleIssueMap({ stale: [staleRow({ issue_id: "" })] });
    expect(map.size).toBe(0);
  });

  it("returns the same Map for the same response (stable across row renders)", () => {
    const data = { stale: [staleRow()] };
    expect(toStaleIssueMap(data)).toBe(toStaleIssueMap(data));
  });
});

describe("staleIssuesOptions", () => {
  it("keys on wsId so switching workspaces swaps the cache entry", () => {
    expect(staleIssuesOptions("ws-a").queryKey).not.toEqual(
      staleIssuesOptions("ws-b").queryKey,
    );
  });

  it("gives the project-filtered read its own key", () => {
    expect(staleIssuesOptions(WS_ID).queryKey).toEqual(stalenessKeys.list(WS_ID));
    expect(staleIssuesOptions(WS_ID, "proj-1").queryKey).not.toEqual(
      staleIssuesOptions(WS_ID).queryKey,
    );
  });

  it("does not retry, and treats the answer as fresh for a minute", () => {
    const opts = staleIssuesOptions(WS_ID);
    expect(opts.retry).toBe(false);
    expect(opts.staleTime).toBe(STALENESS_STALE_TIME_MS);
    expect(opts.enabled).toBe(true);
    expect(staleIssuesOptions("").enabled).toBe(false);
  });
});

describe("WS invalidation", () => {
  function seed() {
    const qc = new QueryClient();
    qc.setQueryData(stalenessKeys.list(WS_ID), { stale: [staleRow()] });
    qc.setQueryData(stalenessKeys.list(WS_ID, "proj-1"), { stale: [] });
    return qc;
  }

  function staleStates(qc: QueryClient) {
    return qc
      .getQueriesData({ queryKey: stalenessKeys.all(WS_ID) })
      .map(([key]) => qc.getQueryState(key)?.isInvalidated);
  }

  it("piggybacks on issue:created", () => {
    const qc = seed();
    onIssueCreated(qc, WS_ID, baseIssue);
    expect(staleStates(qc)).toEqual([true, true]);
  });

  it("piggybacks on issue:updated", () => {
    const qc = seed();
    onIssueUpdated(qc, WS_ID, { id: baseIssue.id, status: "done" });
    expect(staleStates(qc)).toEqual([true, true]);
  });

  it("piggybacks on issue:deleted", () => {
    const qc = seed();
    onIssueDeleted(qc, WS_ID, baseIssue.id);
    expect(staleStates(qc)).toEqual([true, true]);
  });

  it("leaves another workspace's staleness alone", () => {
    const qc = seed();
    qc.setQueryData(stalenessKeys.list("ws-2"), { stale: [] });
    onIssueUpdated(qc, WS_ID, { id: baseIssue.id, status: "done" });
    expect(qc.getQueryState(stalenessKeys.list("ws-2"))?.isInvalidated).toBe(false);
  });
});
