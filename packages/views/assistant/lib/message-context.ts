export interface MessageContext {
  workspace_id: string | null;
  timezone?: string;
  project_id?: string | null;
  member_id?: string | null;
  attachment_ids?: string[];
}

/** The subset of the composer selection that resolves a message's scope. */
interface ComposerSelection {
  workspace_id: string | null;
  workspace_pinned?: boolean;
  project_id?: string | null;
  member?: { user_id: string; name: string } | null;
  attachments?: { id: string }[];
}

/**
 * The workspace this message will actually run in.
 *
 * A workspace PICKED in the composer outranks the page the user happens to be
 * looking at — that is the whole point of the picker — and both outrank the
 * session's focus, which the server falls back to when the message names no
 * workspace at all. One function so the send payload and the "Sending to X"
 * label can never disagree about where a message is going.
 */
export function targetWorkspaceId(
  workspaceId: string | null,
  selection?: ComposerSelection | null,
): string | null {
  return selection?.workspace_pinned && selection.workspace_id
    ? selection.workspace_id
    : workspaceId;
}

export function messageContext(
  workspaceId: string | null,
  selection?: ComposerSelection | null,
): MessageContext {
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const target = targetWorkspaceId(workspaceId, selection);
  // Everything below the workspace is scoped by it. A selection left over from
  // another workspace is dropped rather than sent somewhere it does not
  // resolve — the server would refuse it, and the refusal would be confusing.
  const applies = !!selection && selection.workspace_id === target;
  return {
    workspace_id: target,
    ...(timezone ? { timezone } : {}),
    ...(applies && selection?.project_id ? { project_id: selection.project_id } : {}),
    ...(applies && selection?.member?.user_id ? { member_id: selection.member.user_id } : {}),
    ...(applies && selection?.attachments?.length
      ? { attachment_ids: selection.attachments.map((attachment) => attachment.id) }
      : {}),
  };
}
