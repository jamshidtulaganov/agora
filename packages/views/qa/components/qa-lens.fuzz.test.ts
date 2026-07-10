import { describe, expect, it } from "vitest";
import { verdictFromLabels } from "./qa-lens";

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

// Adversarial label names — the real values plus near-misses that must NOT
// match (case drift, stray whitespace, garbage, unicode).
const LABEL_NAMES = [
  "qa:pass",
  "qa:fail",
  "qa:PASS",
  "QA:FAIL",
  "qa: pass",
  "qa:pass ",
  " qa:fail",
  "qa:stale",
  "bug",
  "design:pass",
  "",
  "🚀",
  "null",
];

function randomNames(rand: () => number): string[] {
  const n = Math.floor(rand() * 10); // 0..9
  return Array.from({ length: n }, () => LABEL_NAMES[Math.floor(rand() * LABEL_NAMES.length)]!);
}

const KNOWN_VERDICTS = ["pass", "fail", "pending"];

describe("verdictFromLabels fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0x7e57e70);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const names = randomNames(rand);

      let verdict: string | undefined;
      try {
        verdict = verdictFromLabels(names);

        // 1. Always a known verdict.
        expect(KNOWN_VERDICTS).toContain(verdict);

        // 2. fail takes UNCONDITIONAL priority over pass — a distinct
        //    contract from qaRowState (qa-lane.tsx), which treats fail+pass
        //    together as an untrustworthy pending, not a fail. Fuzzing both
        //    side by side guards against the two drifting into an
        //    accidentally-shared rule.
        if (names.includes("qa:fail")) {
          expect(verdict).toBe("fail");
        } else if (names.includes("qa:pass")) {
          expect(verdict).toBe("pass");
        } else {
          expect(verdict).toBe("pending");
        }

        // 3. Determinism.
        expect(verdictFromLabels(names)).toBe(verdict);
      } catch (e) {
        throw new Error(`failed on input #${i}: names=${JSON.stringify(names)} verdict=${verdict}\n${String(e)}`);
      }
    }
  });
});
