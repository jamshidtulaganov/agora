import { describe, expect, it } from "vitest";
import { latestQAResultScreenshots, pairDesignScreenshots } from "./screenshots";
import type { Attachment } from "../types";

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

// Adversarial filenames — figma-prefixed (should pair as "figma"), decoys
// that must NOT match the prefix despite looking close, and generally weird
// strings. filename is a required `string` field on Attachment (never
// null/undefined per the type), so every value here stays a string.
const FILENAMES = [
  "",
  "figma-",
  "figma-208-5147.png",
  `figma-🚀-node.png`,
  "FIGMA-1-1.PNG",
  "FiGmA-Mixed-Case.png",
  "notfigma.png",
  "screenshot.png",
  "figma", // no trailing hyphen — must NOT match ^figma-
  "xfigma-1.png", // starts with x — must NOT match
  " figma-1.png", // leading space — anchored ^ must NOT match
  "figma-" + "a".repeat(300) + ".png", // extremely long
  "figma-1.png",
  "figma-1.png", // duplicate name on purpose
  "built.jpg",
  "built.jpg", // duplicate name on purpose
  "IMG_" + "9".repeat(50) + ".webp",
  "..figma-trick.png",
];

let attCounter = 0;

function randomAttachment(rand: () => number): Attachment {
  attCounter += 1;
  return {
    id: `att-${attCounter}`,
    workspace_id: "ws-1",
    issue_id: rand() < 0.5 ? "issue-1" : null,
    comment_id: rand() < 0.5 ? "c1" : null,
    chat_session_id: rand() < 0.5 ? null : "chat-1",
    chat_message_id: rand() < 0.5 ? null : "msg-1",
    uploader_type: pick(rand, ["agent", "member"]),
    uploader_id: "u1",
    filename: pick(rand, FILENAMES),
    url: "https://cdn.example/x",
    download_url: "https://cdn.example/x?sig=1",
    markdown_url: "https://cdn.example/x",
    content_type: pick(rand, [
      "image/png",
      "image/jpeg",
      "image/webp",
      "image/svg+xml",
      "IMAGE/PNG", // wrong-cased — startsWith("image/") is case-sensitive, must NOT count as an image
      "images/png", // decoy — must NOT count
      "image", // no slash — must NOT count
      "text/plain",
      "application/pdf",
      "",
    ]),
    size_bytes: Math.floor(rand() * 10_000_000),
    created_at: "2026-01-01T00:00:00Z",
  };
}

const FIGMA_FILENAME_RE = /^figma-/i;

describe("pairDesignScreenshots fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0xd351611);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const n = Math.floor(rand() * 51); // 0..50 attachments
      const images = Array.from({ length: n }, () => randomAttachment(rand));

      let pairs;
      // Assertions run inside try/catch and skip building any diagnostic
      // string on the hot path — ctx() does a JSON.stringify of up to 50
      // attachments, which is only affordable when something actually
      // failed. This keeps 8k iterations fast (see stage.fuzz.test.ts for
      // the eager-message style, which is fine for its much smaller inputs).
      try {
        pairs = pairDesignScreenshots(images);

        const figmaCount = images.filter((a) => FIGMA_FILENAME_RE.test(a.filename)).length;
        const builtCount = n - figmaCount;

        // 1. Pair-array length is exactly max(figmaCount, builtCount).
        expect(pairs.length).toBe(Math.max(figmaCount, builtCount));

        // 2. Entries with BOTH sides populated ("real" pairs) number exactly
        //    min(figmaCount, builtCount) — the rest degrade to solo entries.
        const fullPairs = pairs.filter((p) => p.figma && p.built).length;
        expect(fullPairs).toBe(Math.min(figmaCount, builtCount));

        // 3. Every input attachment appears EXACTLY once across all pairs
        //    (as .figma or .built, never both, never dropped, never
        //    duplicated) — a bijection between input images and output refs.
        const seenIds = new Set<string>();
        for (const p of pairs) {
          expect(typeof p.key).toBe("string");
          if (p.figma) {
            expect(seenIds.has(p.figma.id)).toBe(false);
            seenIds.add(p.figma.id);
            expect(FIGMA_FILENAME_RE.test(p.figma.filename)).toBe(true);
          }
          if (p.built) {
            expect(seenIds.has(p.built.id)).toBe(false);
            seenIds.add(p.built.id);
            expect(FIGMA_FILENAME_RE.test(p.built.filename)).toBe(false);
          }
        }
        expect(seenIds.size).toBe(n);
        for (const img of images) expect(seenIds.has(img.id)).toBe(true);

        // 4. Determinism.
        expect(pairDesignScreenshots(images)).toEqual(pairs);
      } catch (e) {
        throw new Error(
          `failed on input #${i}: n=${n} images=${JSON.stringify(images.map((a) => ({ id: a.id, filename: a.filename })))} → ${JSON.stringify(pairs)}\n${String(e)}`,
        );
      }
    }
  });
});

// ---------------------------------------------------------------------------

const QA_RESULT_FENCE_RE = /```qa-result\b/;

const AUTHOR_TYPES = ["agent", "member", "system", "", "AGENT", "Agent ", "agent "];

const CONTENTS = [
  "",
  "no fence here",
  "```qa-result\n{}\n```",
  "```qa-result\nbroken json {{{ not valid\n```",
  "``` qa-result\n{}\n```", // space before tag — must NOT match (literal ```qa-result)
  "```qa-result-extra\n{}\n```", // hyphen right after "result" IS a word boundary — DOES match
  "```qa-resultX\n{}\n```", // letter right after "result" is NOT a boundary — must NOT match
  "prefix text\n```qa-result\n{...}\n```\nsuffix text",
  "```json\n{}\n```", // different fence tag — must NOT match
  `🚀 emoji content \`\`\`qa-result\n{}\n\`\`\``,
  "a".repeat(2000), // long garbage, no fence
  "```qa-result```", // no newline at all, still starts with the literal + boundary at end-of-string
];

function randomComment(rand: () => number): {
  author_type: string;
  content: string;
  attachments?: Attachment[];
} {
  const hasAttachments = rand() < 0.85;
  const attachmentCount = Math.floor(rand() * 6);
  return {
    author_type: pick(rand, AUTHOR_TYPES),
    content: pick(rand, CONTENTS),
    attachments: hasAttachments
      ? Array.from({ length: attachmentCount }, () => randomAttachment(rand))
      : undefined,
  };
}

describe("latestQAResultScreenshots fuzz — 8k adversarial inputs", () => {
  it("holds every structural invariant on every input", () => {
    const rand = mulberry32(0x5c0ff33);
    const ITER = 8000;
    for (let i = 0; i < ITER; i++) {
      const m = Math.floor(rand() * 8); // 0..7 comments
      const comments = Array.from({ length: m }, () => randomComment(rand));

      let result: Attachment[] | undefined;
      try {
        result = latestQAResultScreenshots(comments);

        // Independently find the newest (highest-index) agent comment whose
        // content matches the qa-result fence — same scan direction and
        // predicate as the implementation, computed here from scratch so
        // this is a real cross-check, not a restatement.
        let matchIdx = -1;
        for (let ci = comments.length - 1; ci >= 0; ci--) {
          const c = comments[ci]!;
          if (c.author_type === "agent" && QA_RESULT_FENCE_RE.test(c.content ?? "")) {
            matchIdx = ci;
            break;
          }
        }

        // 1. No matching comment → [].
        if (matchIdx === -1) {
          expect(result).toEqual([]);
        } else {
          // 2. Newest-wins: result is exactly that comment's image attachments.
          const expected = (comments[matchIdx]!.attachments ?? []).filter((a) =>
            a.content_type.startsWith("image/"),
          );
          expect(result).toEqual(expected);
        }

        // 3. Every returned attachment is actually image/*.
        for (const a of result) expect(a.content_type.startsWith("image/")).toBe(true);

        // 4. Determinism.
        expect(latestQAResultScreenshots(comments)).toEqual(result);
      } catch (e) {
        throw new Error(
          `failed on input #${i}: comments=${JSON.stringify(comments)} → ${JSON.stringify(result)}\n${String(e)}`,
        );
      }
    }
  });
});
