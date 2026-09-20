import { describe, expect, it } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import {
  DECISION_QUEUE_STALE_TIME_MS,
  decisionItemAgeSeconds,
  decisionQueueKeys,
  decisionQueueOptions,
  isKnownDecisionKind,
  summarizeDecisionQueue,
  usableDecisionItems,
} from "./decision-queue";
import { onIssueCreated, onIssueDeleted, onIssueLabelsChanged, onIssueUpdated } from "./ws-updaters";
import type { DecisionQueueItem, DecisionQueueResponse, Issue } from "../types";

const WS_ID = "ws-1";
const NOW = Date.parse("2026-09-20T12:00:00Z");

function item(over: Partial<DecisionQueueItem> = {}): DecisionQueueItem {
  return {
    kind: "merge_ready",
    issue_id: "issue-1",
    identifier: "MUL-123",
    title: "Export invoices as PDF",
    status: "in_review",
    project_id: "proj-1",
    risk_tier: "guarded",
    risk_tier_source: "risk_map",
    since: "",
    age_hours: 1,
    needed: "Approve the merge",
    needed_code: "merge_approval",
    score: 70,
    stale_reason: "",
    open_pr_count: 1,
    labels: [],
    ...over,
  };
}

function response(over: Partial<DecisionQueueResponse> = {}): DecisionQueueResponse {
  return {
    items: [item()],
    total: 1,
    counts: { escalation: 0, merge_ready: 1, qa_failed: 0, review_failed: 0 },
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

describe("isKnownDecisionKind", () => {
  it("accepts the four kinds this build has copy for", () => {
    expect(isKnownDecisionKind("escalation")).toBe(true);
    expect(isKnownDecisionKind("merge_ready")).toBe(true);
    expect(isKnownDecisionKind("qa_failed")).toBe(true);
    expect(isKnownDecisionKind("review_failed")).toBe(true);
  });

  it("rejects a kind a newer server invented, so the row renders generically", () => {
    expect(isKnownDecisionKind("deploy_approval")).toBe(false);
    expect(isKnownDecisionKind("")).toBe(false);
  });
});

describe("decisionQueueOptions", () => {
  it("keys on wsId so switching workspaces swaps the cache entry", () => {
    expect(decisionQueueOptions("ws-a").queryKey).not.toEqual(
      decisionQueueOptions("ws-b").queryKey,
    );
  });

  it("gives the project-filtered read its own key", () => {
    expect(decisionQueueOptions(WS_ID).queryKey).toEqual(decisionQueueKeys.list(WS_ID));
    expect(decisionQueueOptions(WS_ID, "proj-1").queryKey).not.toEqual(
      decisionQueueOptions(WS_ID).queryKey,
    );
  });

  it("holds the answer for 30s and stays disabled without a workspace", () => {
    expect(decisionQueueOptions(WS_ID).staleTime).toBe(DECISION_QUEUE_STALE_TIME_MS);
    expect(decisionQueueOptions(WS_ID).enabled).toBe(true);
    expect(decisionQueueOptions("").enabled).toBe(false);
  });
});

describe("decisionItemAgeSeconds", () => {
  it("prefers `since`, which keeps counting while the response sits in cache", () => {
    // age_hours would say one minute; the timestamp says two hours, and the
    // timestamp is the one that stays true in a cached response.
    const row = item({ since: "2026-09-20T10:00:00Z", age_hours: 0.016 });
    expect(decisionItemAgeSeconds(row, NOW)).toBe(7200);
  });

  it("falls back to age_hours when the server sent no timestamp", () => {
    expect(decisionItemAgeSeconds(item({ since: "", age_hours: 1.5 }), NOW)).toBe(5400);
  });

  it("reads an unparsable timestamp as the age the server gave", () => {
    expect(decisionItemAgeSeconds(item({ since: "yesterday-ish", age_hours: 3 }), NOW)).toBe(10800);
  });

  it("never reports a negative age from a clock skew", () => {
    const future = item({ since: "2026-09-20T13:00:00Z", age_hours: 0 });
    expect(decisionItemAgeSeconds(future, NOW)).toBe(0);
  });
});

describe("summarizeDecisionQueue", () => {
  it("states the server's exact total and the oldest row's age", () => {
    const s = summarizeDecisionQueue(response(), NOW);
    expect(s).toEqual({ waiting: 1, oldestAgeSeconds: 3600 });
  });

  it("derives the count from the rows when the total is missing or zeroed", () => {
    // A backend that forgets the total must not blank the header.
    const s = summarizeDecisionQueue(
      response({
        items: [item(), item({ issue_id: "issue-2", age_hours: 2 })],
        total: 0,
      }),
      NOW,
    );
    expect(s).toEqual({ waiting: 2, oldestAgeSeconds: 7200 });
  });

  it("keeps a total larger than the listed rows (the list is capped)", () => {
    const s = summarizeDecisionQueue(response({ items: [item()], total: 31 }), NOW);
    expect(s.waiting).toBe(31);
  });

  it("reads undefined as an empty queue, not as an error", () => {
    expect(summarizeDecisionQueue(undefined, NOW)).toEqual({ waiting: 0, oldestAgeSeconds: 0 });
  });
});

describe("usableDecisionItems", () => {
  it("drops a row that cannot be opened", () => {
    const rows = usableDecisionItems(response({ items: [item(), item({ issue_id: "" })] }));
    expect(rows).toHaveLength(1);
  });

  it("keeps a row whose kind this build has never heard of", () => {
    const rows = usableDecisionItems(
      response({ items: [item({ kind: "deploy_approval", issue_id: "issue-9" })] }),
    );
    expect(rows[0]?.kind).toBe("deploy_approval");
  });
});

describe("WS invalidation", () => {
  function seed() {
    const qc = new QueryClient();
    qc.setQueryData(decisionQueueKeys.list(WS_ID), response());
    qc.setQueryData(decisionQueueKeys.list(WS_ID, "proj-1"), response());
    return qc;
  }

  function queueStates(qc: QueryClient) {
    return qc
      .getQueriesData({ queryKey: decisionQueueKeys.all(WS_ID) })
      .map(([key]) => qc.getQueryState(key)?.isInvalidated);
  }

  it("piggybacks on issue:created", () => {
    const qc = seed();
    onIssueCreated(qc, WS_ID, baseIssue);
    expect(queueStates(qc)).toEqual([true, true]);
  });

  it("piggybacks on issue:updated", () => {
    const qc = seed();
    onIssueUpdated(qc, WS_ID, { id: baseIssue.id, status: "in_review" });
    expect(queueStates(qc)).toEqual([true, true]);
  });

  it("piggybacks on issue:deleted", () => {
    const qc = seed();
    onIssueDeleted(qc, WS_ID, baseIssue.id);
    expect(queueStates(qc)).toEqual([true, true]);
  });

  it("piggybacks on issue_labels:changed — a qa:* label IS a verdict", () => {
    const qc = seed();
    onIssueLabelsChanged(qc, WS_ID, baseIssue.id, [
      { id: "l1", workspace_id: WS_ID, name: "qa:fail", color: "#ff0000", created_at: "", updated_at: "" },
    ]);
    expect(queueStates(qc)).toEqual([true, true]);
  });

  it("leaves another workspace's queue alone", () => {
    const qc = seed();
    qc.setQueryData(decisionQueueKeys.list("ws-2"), response());
    onIssueUpdated(qc, WS_ID, { id: baseIssue.id, status: "done" });
    expect(qc.getQueryState(decisionQueueKeys.list("ws-2"))?.isInvalidated).toBe(false);
  });
});
