import { describe, expect, it } from "vitest";
import type { Attachment, TimelineEntry } from "@agora/core/types";
import { designProposalAttachments } from "./design-proposal-attachments";

function attachment(id: string, filename: string, commentId: string): Attachment {
  return {
    id,
    workspace_id: "workspace-1",
    issue_id: "issue-1",
    comment_id: commentId,
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "agent-1",
    filename,
    url: `/uploads/${filename}`,
    download_url: `/api/attachments/${id}/download`,
    markdown_url: "",
    content_type: "image/png",
    size_bytes: 100,
    created_at: "2026-08-11T21:43:21Z",
  };
}

function comment(id: string, createdAt: string, attachments: Attachment[]): TimelineEntry {
  return {
    type: "comment",
    id,
    actor_type: "agent",
    actor_id: "agent-1",
    created_at: createdAt,
    content: "",
    attachments,
  };
}

describe("designProposalAttachments", () => {
  it("lets a schema-correction revision reuse screenshots from an earlier proposal", () => {
    const render = attachment("render-1", "alt1-desktop.png", "proposal-v1");
    const timeline = [
      comment("proposal-v1", "2026-08-11T21:43:21Z", [render]),
      comment("proposal-v2", "2026-08-12T14:05:34Z", []),
    ];

    expect(designProposalAttachments(timeline, "proposal-v2", "2026-08-12T14:05:34Z")).toEqual([
      render,
    ]);
  });

  it("prefers the current revision when an earlier attachment has the same filename", () => {
    const oldRender = attachment("render-old", "preview.png", "proposal-v1");
    const newRender = attachment("render-new", "preview.png", "proposal-v2");
    const timeline = [
      comment("proposal-v1", "2026-08-11T21:43:21Z", [oldRender]),
      comment("proposal-v2", "2026-08-12T14:05:34Z", [newRender]),
    ];

    expect(
      designProposalAttachments(timeline, "proposal-v2", "2026-08-12T14:05:34Z").map(
        (item) => item.id,
      ),
    ).toEqual(["render-new", "render-old"]);
  });

  it("does not leak screenshots uploaded after the selected revision", () => {
    const futureRender = attachment("render-future", "future.png", "proposal-v3");
    const timeline = [
      comment("proposal-v2", "2026-08-12T14:05:34Z", []),
      comment("proposal-v3", "2026-08-13T14:05:34Z", [futureRender]),
    ];

    expect(designProposalAttachments(timeline, "proposal-v2", "2026-08-12T14:05:34Z")).toEqual([]);
  });
});
