import type { QueryClient } from "@tanstack/react-query";
import { assistantKeys } from "./queries";
import type {
  AssistantMessageEventPayload,
  AssistantRunFinishedPayload,
  AssistantToolActivityEventPayload,
} from "../types";

export function invalidateAssistantQueries(qc: QueryClient): void {
  qc.invalidateQueries({ queryKey: assistantKeys.all });
}

function invalidateSessionRun(qc: QueryClient, sessionId: string, runId: string): void {
  qc.invalidateQueries({ queryKey: assistantKeys.session(sessionId) });
  qc.invalidateQueries({ queryKey: assistantKeys.runs(sessionId) });
  qc.invalidateQueries({ queryKey: assistantKeys.run(runId) });
  qc.invalidateQueries({ queryKey: assistantKeys.sessions() });
}

export function onAssistantMessage(qc: QueryClient, payload: AssistantMessageEventPayload): void {
  qc.invalidateQueries({ queryKey: assistantKeys.messages(payload.session_id) });
  invalidateSessionRun(qc, payload.session_id, payload.run_id);
  qc.invalidateQueries({ queryKey: assistantKeys.artifacts() });
  qc.invalidateQueries({ queryKey: assistantKeys.sessionArtifacts(payload.session_id) });
  qc.invalidateQueries({ queryKey: assistantKeys.all, predicate: (query) => query.queryKey[1] === "operation" || query.queryKey[1] === "operations" });
}

export function onAssistantToolActivity(qc: QueryClient, payload: AssistantToolActivityEventPayload): void {
  invalidateSessionRun(qc, payload.session_id, payload.run_id);
}

export function onAssistantRunFinished(qc: QueryClient, payload: AssistantRunFinishedPayload): void {
  qc.invalidateQueries({ queryKey: assistantKeys.messages(payload.session_id) });
  invalidateSessionRun(qc, payload.session_id, payload.run_id);
}
