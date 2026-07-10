import { describe, expect, it } from "vitest";

import { latestQAResultScreenshots, pairDesignScreenshots } from "./screenshots";
import type { Attachment } from "../types";

function att(over: Partial<Attachment> = {}): Attachment {
  return {
    id: "a1",
    workspace_id: "ws-1",
    issue_id: "issue-1",
    comment_id: "c1",
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "agent-1",
    filename: "screenshot.png",
    url: "https://cdn.example/screenshot.png",
    download_url: "https://cdn.example/screenshot.png?sig=1",
    markdown_url: "https://cdn.example/screenshot.png",
    content_type: "image/png",
    size_bytes: 1024,
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

describe("latestQAResultScreenshots", () => {
  it("returns [] when no comment carries a qa-result block", () => {
    expect(
      latestQAResultScreenshots([{ author_type: "agent", content: "just a comment", attachments: [att()] }]),
    ).toEqual([]);
  });

  it("ignores human comments even with a matching fence", () => {
    expect(
      latestQAResultScreenshots([
        { author_type: "member", content: "```qa-result\n{}\n```", attachments: [att()] },
      ]),
    ).toEqual([]);
  });

  it("picks the newest agent qa-result comment's image attachments", () => {
    const older = att({ id: "old-1", filename: "old.png" });
    const newer = att({ id: "new-1", filename: "new.png" });
    const nonImage = att({ id: "log-1", filename: "log.txt", content_type: "text/plain" });
    const result = latestQAResultScreenshots([
      { author_type: "agent", content: "```qa-result\n{}\n```", attachments: [older] },
      { author_type: "member", content: "not it", attachments: [] },
      { author_type: "agent", content: "```qa-result\n{}\n```", attachments: [newer, nonImage] },
    ]);
    expect(result).toEqual([newer]);
  });
});

describe("pairDesignScreenshots", () => {
  it("pairs a figma image with a built image", () => {
    const figma = att({ id: "f1", filename: "figma-208-5147.png" });
    const built = att({ id: "b1", filename: "screenshot.png" });
    const pairs = pairDesignScreenshots([figma, built]);
    expect(pairs).toEqual([{ key: "f1", figma, built }]);
  });

  it("renders unmatched images solo when counts differ", () => {
    const figma1 = att({ id: "f1", filename: "figma-1-1.png" });
    const figma2 = att({ id: "f2", filename: "figma-2-2.png" });
    const built = att({ id: "b1", filename: "screenshot.png" });
    const pairs = pairDesignScreenshots([figma1, figma2, built]);
    expect(pairs).toEqual([
      { key: "f1", figma: figma1, built },
      { key: "f2", figma: figma2, built: undefined },
    ]);
  });

  it("returns [] for an empty image list", () => {
    expect(pairDesignScreenshots([])).toEqual([]);
  });

  it("matches figma filenames case-insensitively", () => {
    const figma = att({ id: "f1", filename: "FIGMA-1-1.PNG" });
    const built = att({ id: "b1", filename: "app-screen.png" });
    expect(pairDesignScreenshots([figma, built])).toEqual([{ key: "f1", figma, built }]);
  });
});
