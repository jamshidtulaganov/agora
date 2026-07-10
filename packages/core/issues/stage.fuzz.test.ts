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

const STAGES: SDLCStage[] = ["design", "dev", "qa", "review"];
const STATUSES = [
  "backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled",
  // enum drift — future/garbage statuses must never crash the pipeline
  "shipped", "", "IN_REVIEW", "🚀", "null", "undefined",
];
const LABELS = [
  "qa:pass", "qa:fail", "qa:blocked", "qa:stale", "merge:override",
  "design:pass", "design:fail",
  "bug", "ci:pass", "ci:fail", "tier:light", "qa:PASS", "qa: pass", "",
];
const GATE_STATES = ["pass", "fail", "pending"] as const;

function randomInput(rand: () => number): StagePipelineInput {
  const pick = <T,>(arr: readonly T[]): T => arr[Math.floor(rand() * arr.length)] as T;
  const maybe = <T,>(v: T): T | undefined => (rand() < 0.5 ? v : undefined);
  const labels = Array.from({ length: Math.floor(rand() * 6) }, () => ({ name: pick(LABELS) }));
  return {
    status: pick(STATUSES),
    labels,
    workMode: maybe(rand() < 0.5 ? "full_pipeline" : "in_editor"),
    prNumber: maybe(rand() < 0.5 ? Math.floor(rand() * 1000) : null),
    hasDesignSignals: rand() < 0.5,
    designVerdict: maybe(rand() < 0.34 ? "pass" : rand() < 0.5 ? "fail" : null),
    qaVerdict: maybe(rand() < 0.34 ? "pass" : rand() < 0.5 ? "fail" : null),
    mergeGates: maybe({ ci: pick(GATE_STATES), qa: pick(GATE_STATES), tier: pick(["trivial", "light", "full", ""]) }),
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

      // 1. Always exactly the 4 stages, in canonical order.
      expect(pipeline.stages.map((s) => s.stage), ctx()).toEqual(STAGES);

      // 2. current is always one of the 4.
      expect(STAGES, ctx()).toContain(pipeline.current);

      // 3. Every state is a known StageState.
      for (const s of pipeline.stages) {
        expect(
          ["pending", "active", "running", "passed", "failed", "blocked", "skipped"],
          ctx(),
        ).toContain(s.state);
      }

      // 4. Skipped-stage rule: no design signals → design skipped
      //    (regardless of everything else).
      const byStage = Object.fromEntries(pipeline.stages.map((s) => [s.stage, s]));
      if (!input.hasDesignSignals && input.status !== "done") {
        expect(byStage.design!.state, ctx()).toBe("skipped");
      }

      // 5. done → every non-skipped stage passed.
      if (input.status === "done") {
        for (const s of pipeline.stages) {
          expect(["passed", "skipped"], ctx()).toContain(s.state);
        }
      }

      // 6. current never points at a skipped stage unless ALL stages are
      //    skipped/passed (terminal fallback = last non-skipped, which only
      //    lands on skipped if literally everything is skipped).
      const cur = byStage[pipeline.current]!;
      if (cur.state === "skipped") {
        const anyOpen = pipeline.stages.some(
          (s) => s.state !== "skipped" && s.state !== "passed",
        );
        expect(anyOpen, ctx()).toBe(false);
      }

      // 7. At most ONE stage got the pending→active promotion (active can also
      //    come from explicit rules: qa active on in_review, review active on
      //    pending gates — so we assert promotion discipline only when the
      //    status rules out explicit actives).
      if (input.status !== "in_review" && !input.mergeGates) {
        const actives = pipeline.stages.filter((s) => s.state === "active");
        expect(actives.length, ctx()).toBeLessThanOrEqual(1);
      }

      // 8. Determinism: same input → same output.
      expect(deriveStagePipeline(input), ctx()).toEqual(pipeline);
    }
  });
});
