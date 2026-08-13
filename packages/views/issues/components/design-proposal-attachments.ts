import type { Attachment, TimelineEntry } from "@agora/core/types";

/**
 * Resolve the evidence available to a design-proposal revision.
 *
 * Schema-correction replies commonly carry only a corrected JSON block while
 * referencing screenshots uploaded on an earlier proposal comment. Prefer
 * attachments on the current comment, then fall back to earlier issue
 * attachments (newest first) so exact render filenames keep resolving.
 */
export function designProposalAttachments(
  timeline: TimelineEntry[],
  commentId: string,
  createdAt: string,
): Attachment[] {
  const proposalTime = Date.parse(createdAt);
  const candidates = timeline
    .filter((entry) => {
      if (entry.type !== "comment" || !entry.attachments?.length) return false;
      if (entry.id === commentId) return true;
      const entryTime = Date.parse(entry.created_at);
      return Number.isFinite(proposalTime) && Number.isFinite(entryTime) && entryTime <= proposalTime;
    })
    .sort((a, b) => {
      if (a.id === commentId) return -1;
      if (b.id === commentId) return 1;
      return Date.parse(b.created_at) - Date.parse(a.created_at);
    });

  const seen = new Set<string>();
  return candidates.flatMap((entry) => entry.attachments ?? []).filter((attachment) => {
    if (seen.has(attachment.id)) return false;
    seen.add(attachment.id);
    return true;
  });
}
