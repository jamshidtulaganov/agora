import { describe, expect, it } from "vitest";
import { messageContext, targetWorkspaceId } from "./message-context";

describe("messageContext", () => {
  it("captures project and attachment IDs only for the selected workspace", () => {
    const selection = {
      workspace_id: "workspace-1",
      project_id: "project-1",
      attachments: [{ id: "file-1" }, { id: "file-2" }],
    };
    expect(messageContext("workspace-1", selection)).toMatchObject({
      workspace_id: "workspace-1",
      project_id: "project-1",
      attachment_ids: ["file-1", "file-2"],
    });
    expect(messageContext("workspace-2", selection)).toEqual(expect.not.objectContaining({
      project_id: "project-1",
    }));
    expect(messageContext("workspace-2", selection).attachment_ids).toBeUndefined();
  });

  // The workspace picker is a per-MESSAGE target: it outranks the page the
  // user is standing on, which is what makes "send this to the other
  // workspace" possible without leaving the conversation.
  it("sends to the picked workspace rather than the page's", () => {
    const pinned = {
      workspace_id: "workspace-2",
      workspace_pinned: true,
      project_id: "project-2",
      attachments: [{ id: "file-9" }],
    };
    expect(targetWorkspaceId("workspace-1", pinned)).toBe("workspace-2");
    expect(messageContext("workspace-1", pinned)).toMatchObject({
      workspace_id: "workspace-2",
      project_id: "project-2",
      attachment_ids: ["file-9"],
    });

    // Without the pin the page still wins — the mirrored workspace_id is not
    // a choice, it is a copy of where the user already is.
    const mirrored = { workspace_id: "workspace-2", project_id: "project-2" };
    expect(targetWorkspaceId("workspace-1", mirrored)).toBe("workspace-1");
    expect(messageContext("workspace-1", mirrored).workspace_id).toBe("workspace-1");
  });

  it("carries an attached member, and drops one left over from another workspace", () => {
    const selection = {
      workspace_id: "workspace-1",
      member: { user_id: "user-7", name: "Dana" },
    };
    expect(messageContext("workspace-1", selection).member_id).toBe("user-7");
    expect(messageContext("workspace-2", selection).member_id).toBeUndefined();
    expect(messageContext("workspace-1", { workspace_id: "workspace-1", member: null }).member_id)
      .toBeUndefined();
    expect(messageContext("workspace-1", null).member_id).toBeUndefined();
  });
});
