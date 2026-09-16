import type { QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { createLogger } from "../logger";
import type { AssistantSession } from "../types";
import { assistantKeys } from "./queries";

const logger = createLogger("assistant.title");

/** Roughly one line in the 256px session rail. */
export const ASSISTANT_TITLE_MAX_LENGTH = 48;

/**
 * Turns the first user message into a session title — no model call, no
 * server round-trip to compute it: a single line, collapsed whitespace, cut
 * on a word boundary.
 *
 * A cut that would leave less than half the budget (a URL, a CJK sentence
 * with no spaces) keeps the hard cut instead, so the title never degrades to
 * a two-character stub.
 */
export function deriveSessionTitle(
  content: string,
  maxLength: number = ASSISTANT_TITLE_MAX_LENGTH,
): string {
  const singleLine = content.replace(/\s+/gu, " ").trim();
  if (singleLine.length <= maxLength) return singleLine;

  const clipped = singleLine.slice(0, maxLength);
  const lastSpace = clipped.lastIndexOf(" ");
  const base = lastSpace >= Math.floor(maxLength / 2) ? clipped.slice(0, lastSpace) : clipped;
  return `${base.trimEnd()}…`;
}

/**
 * Names an untitled session after its first user message.
 *
 * Only ever writes when the cached title is EMPTY, so a manual rename always
 * wins — and a session the list hasn't loaded yet is left alone rather than
 * guessed at. The cache is patched first (the rail renames instantly), then
 * the PATCH goes out; a failure just logs and re-syncs from the server.
 */
export async function autoTitleAssistantSession(
  qc: QueryClient,
  sessionId: string,
  content: string,
): Promise<void> {
  const sessions = qc.getQueryData<AssistantSession[]>(assistantKeys.sessions());
  const session = sessions?.find((item) => item.id === sessionId);
  if (!session || session.title.trim() !== "") return;

  const title = deriveSessionTitle(content);
  if (!title) return;

  const patch = (item: AssistantSession): AssistantSession =>
    item.id === sessionId ? { ...item, title } : item;
  qc.setQueryData<AssistantSession[]>(assistantKeys.sessions(), (old) => old?.map(patch));
  qc.setQueryData<AssistantSession>(assistantKeys.session(sessionId), (old) =>
    old ? { ...old, title } : old,
  );

  try {
    await api.updateAssistantSession(sessionId, { title });
    logger.info("autoTitle.applied", { sessionId });
  } catch (err) {
    logger.warn("autoTitle.failed", { sessionId, err });
  } finally {
    qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
  }
}
