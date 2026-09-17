export interface MessageContext {
  workspace_id: string | null;
  timezone?: string;
  project_id?: string | null;
  attachment_ids?: string[];
}

export function messageContext(
  workspaceId: string | null,
  selection?: { workspace_id: string | null; project_id?: string | null; attachments?: { id: string }[] } | null,
): MessageContext {
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const applies = selection?.workspace_id === workspaceId;
  return {
    workspace_id: workspaceId,
    ...(timezone ? { timezone } : {}),
    ...(applies && selection?.project_id ? { project_id: selection.project_id } : {}),
    ...(applies && selection?.attachments?.length
      ? { attachment_ids: selection.attachments.map((attachment) => attachment.id) }
      : {}),
  };
}
