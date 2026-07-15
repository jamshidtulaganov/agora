import { describe, expect, it } from "vitest";
import { parseArtifactFileDiff } from "./artifact-diff";

const DIFF = `diff --git a/src/old.ts b/src/old.ts
index 1111111..2222222 100644
--- a/src/old.ts
+++ b/src/old.ts
@@ -2,3 +2,4 @@
 keep
-before
+after
+added
 end
diff --git a/src/other.ts b/src/other.ts
index 3333333..4444444 100644
--- a/src/other.ts
+++ b/src/other.ts
@@ -1 +1 @@
-wrong
+right`;

describe("parseArtifactFileDiff", () => {
  it("isolates one file and assigns old and new line numbers", () => {
    const lines = parseArtifactFileDiff(DIFF, "src/old.ts");
    expect(lines.filter((line) => line.kind === "addition")).toEqual([
      { kind: "addition", content: "after", newLine: 3 },
      { kind: "addition", content: "added", newLine: 4 },
    ]);
    expect(lines.find((line) => line.kind === "deletion")).toEqual({
      kind: "deletion",
      content: "before",
      oldLine: 3,
    });
    expect(lines.some((line) => line.content === "wrong")).toBe(false);
  });

  it("returns an empty result when the selected file is absent", () => {
    expect(parseArtifactFileDiff(DIFF, "missing.ts")).toEqual([]);
  });

  it("keeps binary metadata even when there is no hunk", () => {
    const binary = `diff --git a/logo.png b/logo.png
index 1111111..2222222 100644
Binary files a/logo.png and b/logo.png differ`;
    expect(parseArtifactFileDiff(binary, "logo.png").at(-1)).toEqual({
      kind: "meta",
      content: "Binary files a/logo.png and b/logo.png differ",
    });
  });
});
