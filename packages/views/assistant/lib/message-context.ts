export interface MessageContext {
  workspace_id: string | null;
  timezone?: string;
}

export function messageContext(workspaceId: string | null): MessageContext {
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return { workspace_id: workspaceId, ...(timezone ? { timezone } : {}) };
}
