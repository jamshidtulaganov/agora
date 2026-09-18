import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { assistantKeys } from "./queries";
import { autoTitleAssistantSession } from "./auto-title";
import { createLogger } from "../logger";
import type {
  AssistantSession,
  AssistantMessage,
  CreateAssistantSessionRequest,
  SendAssistantMessageRequest,
} from "../types";

const logger = createLogger("assistant.mut");

export function useCreateAssistantSession() {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: (data?: CreateAssistantSessionRequest) => {
      logger.info("createSession.start", { hasFocusWorkspace: !!data?.focus_workspace_id });
      return api.createAssistantSession(data);
    },
    onSuccess: (session) => {
      logger.info("createSession.success", { sessionId: session.id });
      qc.setQueryData<AssistantSession[]>(assistantKeys.sessions(), (current) => {
        const withoutDuplicate = (current ?? []).filter((s) => s.id !== session.id);
        return [session, ...withoutDuplicate];
      });
    },
    onError: (err) => {
      logger.error("createSession.error", err);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
    },
  });
}

/**
 * Renames a session (and/or re-focuses it to a different workspace).
 * Optimistically patches the cached list; rolls back on error. Other
 * tabs/devices catch up via the `assistant:message` / `assistant:run_finished`
 * invalidation (the server has no dedicated "session updated" WS event yet —
 * see docs/agora-assistant-plan.md §5, this mirrors the same gap chat had
 * before chat:session_updated shipped).
 */
export function useUpdateAssistantSession() {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: (data: {
      sessionId: string;
      title?: string;
      /** `null` clears the focus; omit the field to leave it untouched. */
      focus_workspace_id?: string | null;
    }) => {
      const { sessionId, ...patch } = data;
      logger.info("updateSession.start", { sessionId, hasTitle: patch.title !== undefined });
      return api.updateAssistantSession(sessionId, patch);
    },
    onMutate: async ({ sessionId, title, focus_workspace_id }) => {
      await qc.cancelQueries({ queryKey: assistantKeys.sessions() });
      const previous = qc.getQueryData<AssistantSession[]>(assistantKeys.sessions());
      const previousSession = qc.getQueryData<AssistantSession>(assistantKeys.session(sessionId));
      // Both fields are patched the same way: `undefined` means "not part of
      // this request", so only a field that is actually present is written.
      const patch = (session: AssistantSession): AssistantSession => ({
        ...session,
        ...(title !== undefined ? { title } : {}),
        ...(focus_workspace_id !== undefined ? { focus_workspace_id } : {}),
      });
      if (title !== undefined || focus_workspace_id !== undefined) {
        qc.setQueryData<AssistantSession[]>(assistantKeys.sessions(), (old) =>
          old?.map((s) => (s.id === sessionId ? patch(s) : s)),
        );
        qc.setQueryData<AssistantSession>(assistantKeys.session(sessionId), (old) =>
          old ? patch(old) : old,
        );
      }
      return { previous, previousSession };
    },
    onError: (err, vars, ctx) => {
      logger.error("updateSession.error.rollback", { sessionId: vars.sessionId, err });
      if (ctx?.previous) qc.setQueryData(assistantKeys.sessions(), ctx.previous);
      if (ctx?.previousSession) {
        qc.setQueryData(assistantKeys.session(vars.sessionId), ctx.previousSession);
      }
    },
    onSettled: (_data, _err, vars) => {
      qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
      qc.invalidateQueries({ queryKey: assistantKeys.session(vars.sessionId) });
    },
  });
}

/**
 * Hard-deletes a session (server cancels any active run first). Optimistically
 * removes the row from the list and drops its messages cache.
 */
export function useDeleteAssistantSession() {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: (sessionId: string) => {
      logger.info("deleteSession.start", { sessionId });
      return api.deleteAssistantSession(sessionId);
    },
    onMutate: async (sessionId) => {
      await qc.cancelQueries({ queryKey: assistantKeys.sessions() });
      const previous = qc.getQueryData<AssistantSession[]>(assistantKeys.sessions());
      qc.setQueryData<AssistantSession[]>(assistantKeys.sessions(), (old) =>
        old?.filter((s) => s.id !== sessionId),
      );
      qc.removeQueries({ queryKey: assistantKeys.messages(sessionId) });
      return { previous };
    },
    onError: (err, sessionId, ctx) => {
      logger.error("deleteSession.error.rollback", { sessionId, err });
      if (ctx?.previous) qc.setQueryData(assistantKeys.sessions(), ctx.previous);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
    },
  });
}

/**
 * Sends one user turn on `sessionId` and starts the reply loop. Optimistically
 * appends the user's own message to the transcript cache so it renders before
 * the round-trip completes; the server's own `assistant:message` echo (fired
 * for multi-device sync) reconciles it via the WS invalidation in
 * ws-updaters.ts.
 *
 * A 409 means a run is already active on this session — the optimistic
 * message is rolled back and the caller should surface a distinct "still
 * working" toast (check `error instanceof ApiError && error.status === 409`,
 * both exported from `@agora/core/api`).
 */
export function useSendAssistantMessage(sessionId: string) {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: (input: SendAssistantMessageRequest) => {
      logger.info("sendMessage.start", { sessionId, contentLength: input.content.length });
      return api.sendAssistantMessage(sessionId, input);
    },
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: assistantKeys.messages(sessionId) });
      const previous = qc.getQueryData<AssistantMessage[]>(assistantKeys.messages(sessionId));
      // Captured BEFORE the optimistic row lands: auto-titling only ever
      // reacts to the session's very first user turn.
      const isFirstUserMessage = (previous ?? []).every((message) => message.role !== "user");
      const optimistic: AssistantMessage = {
        id: `optimistic-${Date.now()}`,
        session_id: sessionId,
        role: "user",
        content: input.content,
        created_at: new Date().toISOString(),
      };
      qc.setQueryData<AssistantMessage[]>(assistantKeys.messages(sessionId), (old) => [
        ...(old ?? []),
        optimistic,
      ]);
      return { previous, isFirstUserMessage };
    },
    onSuccess: (response, input, ctx) => {
      logger.info("sendMessage.success", { sessionId, runId: response.run_id });
      // "New chat" forever is the single loudest papercut in the rail. The
      // first accepted user turn names the session client-side — no model
      // call — and only while the title is still empty (auto-title.ts).
      if (ctx?.isFirstUserMessage) void autoTitleAssistantSession(qc, sessionId, input.content);
    },
    onError: (err, _content, ctx) => {
      logger.warn("sendMessage.error.rollback", { sessionId, err });
      qc.setQueryData(assistantKeys.messages(sessionId), ctx?.previous);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
      qc.invalidateQueries({ queryKey: assistantKeys.session(sessionId) });
      qc.invalidateQueries({ queryKey: assistantKeys.runs(sessionId) });
      qc.invalidateQueries({ queryKey: assistantKeys.messages(sessionId) });
    },
  });
}

/**
 * Records the human confirmation for a pending destructive operation
 * (`needs_confirmation` tool_result → ConfirmCard → this).
 *
 * Deliberately NOT optimistic: the receipt is a server-persisted `role:"tool"`
 * message that arrives over WS, so there is no local shape to write — the
 * settle invalidation pulls the transcript that now carries it. Errors are
 * re-thrown to the card, which distinguishes 409 (operation changed/expired)
 * from everything else.
 */
export function useConfirmAssistantOperation(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ConfirmAssistantOperationInput) => {
      const { operationId, skippedItems } = normalizeConfirmInput(input);
      logger.info("confirmOperation.start", { sessionId, operationId, skipped: skippedItems.length });
      return api.confirmAssistantOperation(operationId, skippedItems);
    },
    onError: (err, input) => {
      logger.warn("confirmOperation.error", { sessionId, operationId: operationIdOf(input), err });
    },
    onSettled: (_data, _error, input) =>
      invalidateAfterDecision(qc, sessionId, operationIdOf(input)),
  });
}

/**
 * A bare operation id confirms the whole operation — what every single-op
 * ConfirmCard sends, and the request the server has always received. The
 * object form is the PLAN card's: `skippedItems` are the 0-based rows the
 * user unchecked, and an empty list is indistinguishable from the bare form
 * on the wire.
 */
export type ConfirmAssistantOperationInput =
  | string
  | { operationId: string; skippedItems?: readonly number[] };

function normalizeConfirmInput(
  input: ConfirmAssistantOperationInput,
): { operationId: string; skippedItems: readonly number[] } {
  return typeof input === "string"
    ? { operationId: input, skippedItems: [] }
    : { operationId: input.operationId, skippedItems: input.skippedItems ?? [] };
}

function operationIdOf(input: ConfirmAssistantOperationInput): string {
  return typeof input === "string" ? input : input.operationId;
}

/** Declines a pending destructive operation. Same contract as confirm. */
export function useRejectAssistantOperation(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (operationId: string) => {
      logger.info("rejectOperation.start", { sessionId, operationId });
      return api.rejectAssistantOperation(operationId);
    },
    onError: (err, operationId) => {
      logger.warn("rejectOperation.error", { sessionId, operationId, err });
    },
    onSettled: (_data, _error, operationId) => invalidateAfterDecision(qc, sessionId, operationId),
  });
}

/** Both decisions append a tool message and may resume the run that asked. */
function invalidateAfterDecision(qc: ReturnType<typeof useQueryClient>, sessionId: string, operationId: string) {
  qc.invalidateQueries({ queryKey: assistantKeys.operation(operationId) });
  qc.invalidateQueries({ queryKey: assistantKeys.operations(sessionId) });
  qc.invalidateQueries({ queryKey: assistantKeys.messages(sessionId) });
  qc.invalidateQueries({ queryKey: assistantKeys.runs(sessionId) });
  qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
}

/**
 * Requests cancellation and refetches the authoritative run state. The UI
 * stays pending until the server confirms a terminal status.
 */
export function useCancelAssistantRun(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (runId: string) => {
      logger.info("cancelRun.start", { sessionId, runId });
      return api.cancelAssistantRun(runId);
    },
    onError: (err) => {
      logger.warn("cancelRun.error", { sessionId, err });
    },
    onSettled: (_data, _error, runId) => {
      qc.invalidateQueries({ queryKey: assistantKeys.run(runId) });
      qc.invalidateQueries({ queryKey: assistantKeys.runs(sessionId) });
      qc.invalidateQueries({ queryKey: assistantKeys.session(sessionId) });
      qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
    },
  });
}
