export type ArtifactDiffLineKind =
  | "addition"
  | "deletion"
  | "context"
  | "hunk"
  | "meta";

export interface ArtifactDiffLine {
  kind: ArtifactDiffLineKind;
  content: string;
  oldLine?: number;
  newLine?: number;
}

function fileSection(diff: string, path: string): string[] {
  const lines = diff.split("\n");
  const starts: number[] = [];
  for (let index = 0; index < lines.length; index += 1) {
    if (lines[index]?.startsWith("diff --git ")) starts.push(index);
  }
  if (starts.length === 0) return lines;

  for (let sectionIndex = 0; sectionIndex < starts.length; sectionIndex += 1) {
    const start = starts[sectionIndex] ?? 0;
    const end = starts[sectionIndex + 1] ?? lines.length;
    const section = lines.slice(start, end);
    const header = section.slice(0, 8).join("\n");
    if (
      header.includes(`+++ b/${path}`) ||
      header.includes(`--- a/${path}`) ||
      section[0]?.includes(` b/${path}`)
    ) {
      return section;
    }
  }
  return [];
}

export function parseArtifactFileDiff(diff: string, path: string): ArtifactDiffLine[] {
  const lines = fileSection(diff, path);
  const result: ArtifactDiffLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  let inHunk = false;

  for (const line of lines) {
    const hunk = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line);
    if (hunk) {
      oldLine = Number(hunk[1]);
      newLine = Number(hunk[2]);
      inHunk = true;
      result.push({ kind: "hunk", content: line });
      continue;
    }
    if (!inHunk || line.startsWith("\\")) {
      if (line !== "") result.push({ kind: "meta", content: line });
      continue;
    }
    if (line.startsWith("+") && !line.startsWith("+++")) {
      result.push({ kind: "addition", content: line.slice(1), newLine });
      newLine += 1;
      continue;
    }
    if (line.startsWith("-") && !line.startsWith("---")) {
      result.push({ kind: "deletion", content: line.slice(1), oldLine });
      oldLine += 1;
      continue;
    }
    result.push({
      kind: "context",
      content: line.startsWith(" ") ? line.slice(1) : line,
      oldLine,
      newLine,
    });
    oldLine += 1;
    newLine += 1;
  }
  return result;
}
