import { describe, expect, it } from "vitest";
import type { Issue, Label } from "@agora/core/types";
import { qaRowState, type QARowState } from "./qa-lane";

// Deterministic PRNG (mulberry32) — reproducible fuzz runs; the seed is fixed
// so a failure here is a failure every time, not a flake. Mirrors
// packages/core/issues/stage.fuzz.test.ts.
function mulberry32(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function pick<T>(rand: () => number, arr: readonly T[]): T {
  return arr[Math.floor(rand() * arr.length)] as T;
}

// Adversarial label names — the real values plus near-misses that must NOT
// match (case drift, stray whitespace, garbage, unicode).
const LABEL_NAMES = [
  "qa:pass",
  "qa:fail",
  "qa:stale",
  "qa:PASS",
  "QA:FAIL",
  "qa: pass",
  "qa:pass ",
  " qa:fail",
  "qa:passfail",
  "qa:fail-override",
  "qa:stale-legacy",
  "bug",
  "design:pass",
  "",
  "🚀",
  "null",
  "undefined",
];

let labelCounter = 0;

function randomLabel(rand: () => number): Label {
  labelCounter += 1;
  return {
    id: `lbl-${labelCounter}`,
    workspace_id: "ws-1",
    name: pick(rand, LABEL_NAMES),
    color: "#3b82f6",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function randomIssue(rand: () => number): Issue {
  const labelCount = Math.floor(rand() * 8); // 0..7
  const labels = Array.from({ length: labelCount }, () => randomLabel(rand));
  return {
    id: "issue-1",
    workspace_id: "ws-1",
    number: 1,
    identifier: "MUL-1",
    title: "t",
    description: null,
    status: "in_review",
    priority: "medium",
    assignee_type: null,
    assignee_id: null,
    creator_type: "member",
    creator_id: "u1",
    parent_issue_id: null,
    project_id: null,
    position: 0,
    start_date: null,
    due_date: null,
    metadata: {},
    // Sometimes omit labels entirely — the type is optional and the function
    // must tolerate it.
    labels: rand() < 0.9 ? labels : undefined,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

const KNOWN_STATES: QARowState[] = ["running", "stale", "fail", "pass", "pending"];

describe("qaRowState fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0x9a51170);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const issue = randomIssue(rand);
      const isLive = rand() < 0.5;

      let state: QARowState | undefined;
      try {
        state = qaRowState(issue, isLive);

        // 1. Always a known state.
        expect(KNOWN_STATES).toContain(state);

        // 2. isLive always wins, regardless of labels.
        if (isLive) {
          expect(state).toBe("running");
        } else {
          const names = (issue.labels ?? []).map((l) => l.name);
          const fail = names.includes("qa:fail");
          const pass = names.includes("qa:pass");

          // 3. qa:stale takes precedence over fail/pass when not live.
          if (names.includes("qa:stale")) {
            expect(state).toBe("stale");
          } else if (fail && pass) {
            // 4. Conflicting fail+pass (without stale) degrades to pending —
            //    an untrustworthy legacy pairing, never a real verdict.
            expect(state).toBe("pending");
          } else if (fail) {
            expect(state).toBe("fail");
          } else if (pass) {
            expect(state).toBe("pass");
          } else {
            expect(state).toBe("pending");
          }
        }

        // 5. Determinism.
        expect(qaRowState(issue, isLive)).toBe(state);
      } catch (e) {
        throw new Error(
          `failed on input #${i}: labels=${JSON.stringify(issue.labels?.map((l) => l.name))} isLive=${isLive} state=${state}\n${String(e)}`,
        );
      }
    }
  });
});
