import { describe, expect, it } from "vitest";
import {
  deployEnvironmentRequiresHuman,
  parseDeployEnvironments,
  type DeployEnvironment,
} from "./schemas";

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

// ---------------------------------------------------------------------------
// parseDeployEnvironments(settings: unknown)
// ---------------------------------------------------------------------------

const KEY_POOL = [
  "production",
  "prod",
  "PRODUCTION",
  "Production",
  " prod ",
  "staging",
  "preprod",
  "",
  "   ",
  "dev",
  "prod-1",
  "🚀prod",
  "a".repeat(500),
];

function isPlainObject(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// Independent oracle mirroring DeployEnvironmentSchema's actual zod
// semantics (verified empirically against the real schema — see session
// notes): the entry must be a plain object; `key`/`label`/`kind`, if
// present, must be strings; `requires_human`, if present, must be a
// boolean. Unlike those four fields, `target` can never fail the entry —
// DeployEnvironmentTargetSchema is `.default(EMPTY).catch(EMPTY)`, so any
// shape (including totally wrong types) degrades to the empty target
// instead of rejecting the entry. Extra unknown fields are allowed
// (`.loose()`).
function predictEntry(item: unknown): { key: string; requires_human: boolean } | null {
  if (!isPlainObject(item)) return null;
  for (const field of ["key", "label", "kind"] as const) {
    if (item[field] !== undefined && typeof item[field] !== "string") return null;
  }
  if (item.requires_human !== undefined && typeof item.requires_human !== "boolean") return null;
  const key = typeof item.key === "string" ? item.key : "";
  const requires_human = typeof item.requires_human === "boolean" ? item.requires_human : false;
  if (key.trim() === "") return null; // keyless entries are dropped
  return { key, requires_human };
}

function randomDeployEnvEntry(rand: () => number): unknown {
  const kind = Math.floor(rand() * 10);
  switch (kind) {
    case 0:
      return null;
    case 1:
      return "just a string";
    case 2:
      return 42;
    case 3:
      return []; // array as entry — zod rejects (not a plain object)
    case 4:
      return {}; // empty object — key defaults to "" → dropped
    case 5:
      return { key: pick(rand, KEY_POOL) };
    case 6:
      return {
        key: pick(rand, KEY_POOL),
        label: rand() < 0.5 ? "Label" : 123, // sometimes wrong type → whole entry rejected
        requires_human: rand() < 0.5 ? rand() < 0.5 : "yes", // sometimes wrong type → rejected
        target:
          rand() < 0.3
            ? "not-an-object"
            : rand() < 0.6
              ? { kind: "gitlab_pipeline", project_path: 5 } // malformed sub-field — caught, not rejected
              : { kind: "tier2", command: "deploy.sh" },
      };
    case 7:
      return { key: 999 }; // wrong-type key → whole entry rejected
    case 8:
      return { key: pick(rand, KEY_POOL), unknown_extra_field: { a: 1 } }; // .loose() tolerates this
    case 9:
      return { key: pick(rand, KEY_POOL), requires_human: null }; // present-but-null → rejected (not "absent")
    default:
      return { key: pick(rand, KEY_POOL), requires_human: true };
  }
}

function randomDeployEnvironmentsArray(rand: () => number): unknown[] {
  const n = Math.floor(rand() * 12); // 0..11 entries
  return Array.from({ length: n }, () => randomDeployEnvEntry(rand));
}

function randomNonArrayGarbage(rand: () => number): unknown {
  return pick(rand, [null, "str", 123, {}, true, undefined, { nested: "obj" }]);
}

function randomSettings(rand: () => number): unknown {
  const shapeKind = Math.floor(rand() * 8);
  switch (shapeKind) {
    case 0:
      return null;
    case 1:
      return undefined;
    case 2:
      return "a bare string, not an object";
    case 3:
      return 12345;
    case 4:
      return []; // top-level array — no .deploy_environments prop → []
    case 5:
      return { deploy_environments: randomDeployEnvironmentsArray(rand) };
    case 6:
      return { deploy_environments: randomNonArrayGarbage(rand) };
    default:
      return { other_field: "x" }; // deploy_environments key entirely missing
  }
}

describe("parseDeployEnvironments fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0xdeb10c1);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const settings = randomSettings(rand);

      let result: DeployEnvironment[] | undefined;
      try {
        result = parseDeployEnvironments(settings);

        // 1. Always an array.
        expect(Array.isArray(result)).toBe(true);

        // 2. Every returned entry has a non-blank key and a boolean requires_human.
        for (const env of result) {
          expect(env.key.trim()).not.toBe("");
          expect(typeof env.requires_human).toBe("boolean");
          expect(typeof env.label).toBe("string");
          expect(typeof env.kind).toBe("string");
          expect(typeof env.target).toBe("object");
        }

        // 3. Oracle cross-check against the raw array, when there is one.
        const raw =
          isPlainObject(settings) && Array.isArray((settings as { deploy_environments?: unknown }).deploy_environments)
            ? ((settings as { deploy_environments?: unknown }).deploy_environments as unknown[])
            : null;

        if (raw === null) {
          // 4. Malformed / absent settings.deploy_environments → [].
          expect(result).toEqual([]);
        } else {
          const expected = raw.map(predictEntry).filter((e): e is { key: string; requires_human: boolean } => e !== null);
          expect(result.length).toBe(expected.length);
          for (let j = 0; j < expected.length; j++) {
            expect(result[j]!.key).toBe(expected[j]!.key);
            expect(result[j]!.requires_human).toBe(expected[j]!.requires_human);
          }
          // 5. Never invents entries beyond what was in the raw array.
          expect(result.length).toBeLessThanOrEqual(raw.length);
        }

        // 6. Determinism.
        expect(parseDeployEnvironments(settings)).toEqual(result);

        // 7. Integration: every entry this function returns must survive
        //    deployEnvironmentRequiresHuman without throwing.
        for (const env of result) {
          expect(() => deployEnvironmentRequiresHuman(env)).not.toThrow();
        }
      } catch (e) {
        throw new Error(
          `failed on input #${i}: settings=${JSON.stringify(settings)} → ${JSON.stringify(result)}\n${String(e)}`,
        );
      }
    }
  });
});

// ---------------------------------------------------------------------------
// deployEnvironmentRequiresHuman(env: DeployEnvironment)
// ---------------------------------------------------------------------------

const EMPTY_TARGET = { kind: "", project_path: "", ref: "", environment: "", command: "" };

const REQUIRES_HUMAN_KEY_POOL = [
  "production",
  "prod",
  "PRODUCTION",
  "Production",
  " prod",
  "prod ",
  " PROD ",
  "production\n",
  "prod-1",
  "preprod",
  "staging",
  "dev",
  "",
  "   ",
  "🚀prod",
];

function randomDeployEnvironment(rand: () => number): DeployEnvironment {
  return {
    key: pick(rand, REQUIRES_HUMAN_KEY_POOL),
    label: "",
    kind: "",
    requires_human: rand() < 0.3,
    target: EMPTY_TARGET,
  };
}

// Independent oracle mirroring deployEnvironmentRequiresHuman's documented
// contract: the explicit flag, OR a production-named key (case/whitespace
// insensitive) as defense in depth.
function requiresHumanOracle(env: DeployEnvironment): boolean {
  if (env.requires_human) return true;
  const key = env.key.trim().toLowerCase();
  return key === "production" || key === "prod";
}

describe("deployEnvironmentRequiresHuman fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0x40446a);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const env = randomDeployEnvironment(rand);

      let result: boolean;
      try {
        result = deployEnvironmentRequiresHuman(env);
      } catch (e) {
        throw new Error(`threw on input #${i}: env=${JSON.stringify(env)}\n${String(e)}`);
      }
      const ctx = () => `input #${i}: env=${JSON.stringify(env)} → ${result}`;

      // 1. Always boolean.
      expect(typeof result, ctx()).toBe("boolean");

      // 2. Full oracle match.
      expect(result, ctx()).toBe(requiresHumanOracle(env));

      // 3. requires_human is never true unless EITHER the flag is explicitly
      //    true OR the trimmed/lowercased key is exactly "production"/"prod"
      //    — never a fuzzy/substring match (e.g. "preprod" must be false).
      if (result) {
        const key = env.key.trim().toLowerCase();
        expect(env.requires_human === true || key === "production" || key === "prod", ctx()).toBe(true);
      }
      if (env.key.trim().toLowerCase() === "preprod" && !env.requires_human) {
        expect(result, ctx()).toBe(false);
      }

      // 4. Determinism.
      expect(deployEnvironmentRequiresHuman(env), ctx()).toBe(result);
    }
  });
});
