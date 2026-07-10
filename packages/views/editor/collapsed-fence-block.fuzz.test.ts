import { describe, expect, it } from "vitest";
import { COLLAPSE_LINE_THRESHOLD, shouldCollapseFence } from "./collapsed-fence-block";

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

// Mirrors the private RAW_DATA_FENCE_TAGS set in collapsed-fence-block.tsx —
// duplicated here because the collapse predicate's tag list isn't exported.
// Keep this in sync if that set ever changes.
const KNOWN_RAW_DATA_TAGS = new Set([
  "json",
  "test-runs",
  "scripts",
  "qa-result",
  "deploy-result",
  "results",
]);

const LANGS: (string | undefined)[] = [
  undefined,
  "",
  "json",
  "JSON",
  "Json",
  " json", // leading space — Set.has is exact, must NOT match
  "json ", // trailing space — must NOT match
  "test-runs",
  "TEST-RUNS",
  "scripts",
  "qa-result",
  "QA-RESULT",
  "deploy-result",
  "results",
  "js",
  "ts",
  "tsx",
  "python",
  "go",
  "plaintext",
  "markdown",
  "html",
  "mermaid",
  "yaml",
  "sql",
  "🚀lang",
  "a".repeat(200),
  "json-ish", // close to a tag but not one
  "jsonl",
  "x",
];

// Independent oracle for "does this body look like a JSON payload" — mirrors
// looksLikeJsonPayload's contract (starts with `{`/`[` after trim, and
// JSON.parse succeeds), implemented separately so this is a real
// cross-check rather than a restatement.
function looksLikeJsonOracle(code: string): boolean {
  const trimmed = code.trim();
  if (!trimmed || !(trimmed.startsWith("{") || trimmed.startsWith("["))) return false;
  try {
    JSON.parse(trimmed);
    return true;
  } catch {
    return false;
  }
}

// Independent oracle for line counting — mirrors countLines's contract
// (strip exactly one trailing newline, then split).
function countLinesOracle(code: string): number {
  return code.replace(/\n$/, "").split("\n").length;
}

function randomBody(rand: () => number): string {
  const lineCount = Math.floor(rand() * 501); // 0..500
  const kind = Math.floor(rand() * 6);

  if (kind === 0) {
    // Valid JSON object.
    const entries = Array.from({ length: Math.max(0, lineCount - 2) }, (_, i) => `  "k${i}": ${i}`);
    return `{\n${entries.join(",\n")}\n}`;
  }
  if (kind === 1) {
    // Valid JSON array.
    const entries = Array.from({ length: Math.max(0, lineCount - 2) }, (_, i) => `  ${i}`);
    return `[\n${entries.join(",\n")}\n]`;
  }
  if (kind === 2) {
    // Broken JSON — opens with `{` but never validly closes/parses.
    const lines = Array.from({ length: lineCount }, (_, i) => `  garbage${i} === unterminated`);
    return `{\n${lines.join("\n")}`;
  }
  if (kind === 3) {
    // Plain source code / prose — never JSON-shaped.
    const lines = Array.from({ length: lineCount }, (_, i) => `function line${i}() { return ${i}; }`);
    return lines.join("\n");
  }
  if (kind === 4) {
    // Nested backticks + unicode noise.
    const lines = Array.from({ length: lineCount }, (_, i) => "```nested``` 🚀 line " + i);
    return lines.join("\n");
  }
  // Empty / whitespace-only.
  return rand() < 0.5 ? "" : Array.from({ length: lineCount }, () => "   ").join("\n");
}

describe("shouldCollapseFence fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0x5c0110a);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const lang = LANGS[Math.floor(rand() * LANGS.length)];
      const code = randomBody(rand);

      let result: boolean | undefined;
      try {
        result = shouldCollapseFence(lang, code);

        const lines = countLinesOracle(code);
        const isTaggedRawData = !!lang && KNOWN_RAW_DATA_TAGS.has(lang.toLowerCase());
        const isJsonShaped = looksLikeJsonOracle(code);
        const expected = lines > COLLAPSE_LINE_THRESHOLD && (isTaggedRawData || isJsonShaped);

        // 1. Full oracle match — independently derived expectation.
        expect(result).toBe(expected);

        // 2. Fences at or under the threshold never collapse, regardless of
        //    lang or content.
        if (lines <= COLLAPSE_LINE_THRESHOLD) {
          expect(result).toBe(false);
        }

        // 3. Non-JSON-shaped code with a lang that isn't a known raw-data tag
        //    never collapses (a long human-pasted source snippet is untouched).
        if (!isTaggedRawData && !isJsonShaped) {
          expect(result).toBe(false);
        }

        // 4. Determinism.
        expect(shouldCollapseFence(lang, code)).toBe(result);
      } catch (e) {
        throw new Error(
          `failed on input #${i}: lang=${JSON.stringify(lang)} lines=${countLinesOracle(code)} result=${result} code=${JSON.stringify(code.slice(0, 200))}\n${String(e)}`,
        );
      }
    }
  });
});
