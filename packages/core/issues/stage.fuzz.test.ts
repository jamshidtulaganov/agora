import { describe, it, expect } from "vitest";
import { deriveStagePipeline, type SDLCStage, type StagePipelineInput } from "./stage";

// Deterministic PRNG (mulberry32) — reproducible fuzz runs; the seed is fixed
// so a failure here is a failure every time, not a flake.
function mulberry32(seed: number) {
  return () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const STAGES: SDLCStage[] = ["dev", "review"];
const STATUSES = [
  "backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled",
  // enum drift — future/garbage statuses must never crash the pipeline
  "shipped", "", "IN_REVIEW", "🚀", "null", "undefined",
];
const LABELS = [
  "qa:pass", "qa:fail", "qa:blocked", "qa:stale", "merge:override",
  "review:pass", "review:fail", "merge:approved",
  "bug", "ci:pass", "ci:fail", "tier:light", "qa:PASS", "qa: pass", "",
];
function randomInput(rand: () => number): StagePipelineInput {
  const pick = <T,>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)] as T;
  const maybe = <T,>(v: T): T | undefined => (rand() < 0.5 ? v : undefined);
  const labels = Array.from({ length: Math.floor(rand() * 6) }, () => ({ name: pick(LABELS) }));
  return {
    status: pick(STATUSES),
    labels,
    prNumber: maybe(rand() < 0.5 ? Math.floor(rand() * 1000) : null),
    gateFailed: maybe(rand() < 0.5),
    prMerged: maybe(rand() < 0.5),
    runningTaskStages: maybe(
      Array.from({ length: Math.floor(rand() * 3) }, () => pick(STAGES)),
    ),
  };
}

describe("deriveStagePipeline fuzz — 10k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0xa60a7a);
    for (let i = 0; i < 10_000; i++) {
      const input = randomInput(rand);
      let pipeline;
      try {
        pipeline = deriveStagePipeline(input);
      } catch (e) {
        throw new Error(`threw on input #${i}: ${JSON.stringify(input)}\n${String(e)}`);
      }
      const ctx = () => `input #${i}: ${JSON.stringify(input)} → ${JSON.stringify(pipeline)}`;

      // 1. Always exactly the 2 stages, in canonical order.
      expect(pipeline.stages.map((s) => s.stage), ctx()).toEqual(STAGES);

      // 2. current is always one of the 2.
      expect(STAGES, ctx()).toContain(pipeline.current);

      // 3. Every state is a known StageState.
      for (const s of pipeline.stages) {
        expect(
          ["pending", "active", "running", "passed", "failed", "blocked", "skipped"],
          ctx(),
        ).toContain(s.state);
      }

      // 4. done → every stage passed (no stage in this 2-stage model ever
      //    derives to "skipped").
      if (input.status === "done") {
        for (const s of pipeline.stages) {
          expect(s.state, ctx()).toBe("passed");
        }
      }

      // 5. current never points at a skipped stage unless ALL stages are
      //    skipped/passed (terminal fallback = last non-skipped, which only
      //    lands on skipped if literally everything is skipped).
      const byStage = Object.fromEntries(pipeline.stages.map((s) => [s.stage, s]));
      const cur = byStage[pipeline.current]!;
      if (cur.state === "skipped") {
        const anyOpen = pipeline.stages.some(
          (s) => s.state !== "skipped" && s.state !== "passed",
        );
        expect(anyOpen, ctx()).toBe(false);
      }

      // 6. At most ONE stage got the pending→active promotion (active can also
      //    come from explicit rules: review active on in_review / review:pass
      //    "awaiting approval" / merge:approved — so we assert promotion
      //    discipline only when the input rules out every explicit active).
      const labelSet = new Set(input.labels.map((l) => l.name));
      const reviewExplicitActive =
        labelSet.has("merge:approved") || labelSet.has("review:pass");
      if (input.status !== "in_review" && !reviewExplicitActive) {
        const actives = pipeline.stages.filter((s) => s.state === "active");
        expect(actives.length, ctx()).toBeLessThanOrEqual(1);
      }

      // 7. Determinism: same input → same output.
      expect(deriveStagePipeline(input), ctx()).toEqual(pipeline);
    }
  });
});
