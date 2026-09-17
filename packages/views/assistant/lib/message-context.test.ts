import { describe, expect, it } from "vitest";
import { messageContext } from "./message-context";

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
});
