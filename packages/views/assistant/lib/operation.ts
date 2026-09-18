// Pure decoders for the three tool_result shapes the confirmation-binding
// contract adds (docs/agora-assistant-final-plan.md, "Pinned wire contract"):
//
//  1. `{"status":"needs_confirmation","operation":{…}}` — a destructive call
//     waiting for an out-of-band human click. Renders a ConfirmCard.
//  2. `{"receipt":{"action","target","links":[],"effects":[]}}` — what a
//     successful mutation actually did. Renders an upgraded chip.
//  3. `{"status":"uncertain","inspect":"…"}` — the effect may or may not have
//     landed. Renders a warning-toned chip and is never blindly replayed.
//
// `tool_result` is arbitrary tool-specific JSON (zod deliberately keeps it as
// `z.unknown()` — see packages/core/api/schemas.ts), so every function here
// returns `null` on anything it doesn't fully recognise and the caller falls
// back to the plain ToolChip. That is the whole contract of this file: an
// older server that never sends these shapes, and a newer one that renames a
// field, both degrade to the transcript we already ship instead of throwing
// into the message list. Same rule as lib/artifact.ts.

import type { AssistantMessage } from "@agora/core/types";
import type { AssistantOperation } from "@agora/core/types";

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function str(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

/** First non-empty string among the given fields of `obj`. */
function pick(obj: Record<string, unknown>, fields: readonly string[]): string {
  for (const field of fields) {
    const value = str(obj[field]);
    if (value) return value;
  }
  return "";
}

// --- target -------------------------------------------------------------

/** What an operation or receipt points at: `{type, identifier, title}`. */
export interface OperationTarget {
  /** "issue" / "member" / … — free-form, only used for the aria label. */
  type: string;
  /** Human-readable id the user recognises (e.g. `MUL-123`). */
  identifier: string;
  title: string;
}

/**
 * Decode a target. Accepts the pinned object shape and, defensively, a bare
 * string (a server that flattened the field): a lone string is a label, so it
 * lands in `title` where it is safe to show. Returns null when nothing
 * identifiable is present — the card/chip then simply omits the target line
 * rather than rendering an empty one.
 */
export function parseOperationTarget(value: unknown): OperationTarget | null {
  const bare = str(value);
  if (bare) return { type: "", identifier: "", title: bare };

  const obj = asRecord(value);
  if (!obj) return null;

  const target: OperationTarget = {
    type: pick(obj, ["type", "kind", "entity"]),
    identifier: pick(obj, ["identifier", "key", "id"]),
    title: pick(obj, ["title", "name", "label"]),
  };
  return target.identifier || target.title ? target : null;
}

/** One line for a target: `MUL-123 · Fix the login redirect`. */
export function targetLabel(target: OperationTarget | null): string {
  if (!target) return "";
  return [target.identifier, target.title].filter(Boolean).join(" · ");
}

// --- needs_confirmation -------------------------------------------------

export interface ConfirmationRequest {
  operationId: string;
  toolName: string;
  /** One-line description of what confirming will do. May be empty. */
  summary: string;
  /** Workspace the operation runs in — shown so scope is never implicit. */
  workspaceSlug: string;
  target: OperationTarget | null;
  /**
   * "plan" for a batch operation (docs/assistant-domain-plan.md, "3a wire
   * contract"). ABSENT — hence "" — on every single-call operation, including
   * everything a runtime that predates plans sends, which is what makes the
   * ConfirmCard the default rendering.
   */
  kind: string;
  /** Plan rows. Empty for a single operation AND for a malformed list: the
   *  caller then renders the single-op card instead of a broken checklist. */
  items: PlanItem[];
}

/** One proposed row of a plan: what it will do, and with which tool. */
export interface PlanItem {
  /** 0-based position — the index `skipped_items` refers to. */
  index: number;
  tool: string;
  summary: string;
}

/**
 * Decode a `needs_confirmation` result. The operation **id** is the only
 * strictly required field: without it there is nothing the Confirm button
 * could bind to, so the row degrades to a plain chip rather than offering a
 * button that cannot work.
 */
export function parseConfirmationRequest(result: unknown): ConfirmationRequest | null {
  const obj = asRecord(result);
  if (!obj || str(obj.status) !== "needs_confirmation") return null;

  const operation = asRecord(obj.operation);
  if (!operation) return null;

  const operationId = pick(operation, ["id", "operation_id"]);
  if (!operationId) return null;

  return {
    operationId,
    toolName: pick(operation, ["tool_name", "tool"]),
    summary: pick(operation, ["summary", "description"]),
    workspaceSlug: pick(operation, ["workspace_slug", "workspace"]),
    target: parseOperationTarget(operation.target),
    kind: pick(operation, ["kind"]),
    items: parsePlanItems(operation.items),
  };
}

// --- plan rows ----------------------------------------------------------

/**
 * Decode the proposal rows of a plan operation.
 *
 * Anything that is not a list of recognisable rows decodes to `[]` — a plan
 * card with no rows is worse than the single-operation card, so the caller
 * treats an empty list as "render the ConfirmCard". A row keeps its SERVER
 * index (falling back to its position) because that index is what
 * `skipped_items` names; a row with neither a summary nor a tool is dropped
 * rather than rendered as an empty checkbox.
 */
export function parsePlanItems(value: unknown): PlanItem[] {
  if (!Array.isArray(value)) return [];
  const items: PlanItem[] = [];
  value.forEach((entry, position) => {
    const obj = asRecord(entry);
    if (!obj) return;
    const tool = pick(obj, ["tool", "tool_name"]);
    const summary = pick(obj, ["summary", "description", "title"]);
    if (!tool && !summary) return;
    items.push({ index: intOr(obj.index, position), tool, summary });
  });
  return items;
}

/** Outcome of one executed plan row. Anything the server sends that this
 *  build doesn't know becomes "unknown" and renders neutrally. */
export type PlanItemOutcome = "ok" | "failed" | "skipped" | "not_run" | "unknown";

export interface PlanItemResult {
  index: number;
  outcome: PlanItemOutcome;
  /** Identifier the row produced or touched, e.g. `MUL-123`. May be empty. */
  identifier: string;
  error: string;
}

const KNOWN_OUTCOMES = new Set<PlanItemOutcome>(["ok", "failed", "skipped", "not_run"]);

/**
 * Decode the per-row execution outcomes of a confirmed plan (the confirm
 * response body and, after a reload, the stored receipt — same shape).
 *
 * A row with no `outcome` at all is a PROPOSAL, not a result (the same
 * `items` field carries both), so it is skipped: the card keeps that row
 * pending instead of inventing a verdict for it. A row with an outcome this
 * build doesn't recognise is kept as "unknown" — enum drift downgrades to a
 * neutral glyph, it never crashes the receipt.
 */
export function parsePlanItemResults(value: unknown): PlanItemResult[] {
  if (!Array.isArray(value)) return [];
  const results: PlanItemResult[] = [];
  value.forEach((entry, position) => {
    const obj = asRecord(entry);
    if (!obj) return;
    const outcome = str(obj.outcome).toLowerCase();
    if (!outcome) return;
    results.push({
      index: intOr(obj.index, position),
      outcome: KNOWN_OUTCOMES.has(outcome as PlanItemOutcome) ? (outcome as PlanItemOutcome) : "unknown",
      identifier: pick(obj, ["identifier", "key", "id"]),
      error: pick(obj, ["error", "message", "detail"]),
    });
  });
  return results;
}

/** A finite integer field, or the fallback when the server sent something
 *  else (a string index, a float, nothing at all). */
function intOr(value: unknown, fallback: number): number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : fallback;
}

// --- receipts -----------------------------------------------------------

export interface ReceiptLink {
  label: string;
  /** Always app-relative (starts with "/") — see parseReceipt. */
  href: string;
}

export interface Receipt {
  /** What happened, e.g. "Assigned issue". Required. */
  action: string;
  target: OperationTarget | null;
  links: ReceiptLink[];
  /** Downstream consequences, one line each. */
  effects: string[];
}

const LINK_URL_FIELDS = ["url", "href", "path"] as const;
const LINK_LABEL_FIELDS = ["label", "title", "text", "name"] as const;
const EFFECT_FIELDS = ["description", "summary", "label", "text", "detail"] as const;

/**
 * Decode `{"receipt":{…}}` off a successful mutation result.
 *
 * `action` is required: a receipt with no action is an unrecognised shape, and
 * an upgraded chip with a blank headline is worse than the generic one.
 *
 * Links are filtered to **app-relative** hrefs. They render through `AppLink`,
 * which routes in-app; an off-site URL is not part of the pinned contract and
 * would silently push a garbage route.
 */
export function parseReceipt(result: unknown): Receipt | null {
  const obj = asRecord(result);
  if (!obj) return null;
  // A pending confirmation is not a receipt, whatever else it carries.
  if (str(obj.status) === "needs_confirmation") return null;

  const receipt = asRecord(obj.receipt);
  if (!receipt) return null;

  const action = pick(receipt, ["action", "summary", "title"]);
  if (!action) return null;

  return {
    action,
    target: parseOperationTarget(receipt.target),
    links: parseReceiptLinks(receipt.links),
    effects: parseReceiptEffects(receipt.effects),
  };
}

function parseReceiptLinks(value: unknown): ReceiptLink[] {
  if (!Array.isArray(value)) return [];
  const links: ReceiptLink[] = [];
  for (const entry of value) {
    const bare = str(entry);
    if (bare) {
      // The server ships plain `url_path` strings; a raw path reads badly in
      // a chip, so the last segment (the identifier) becomes the label.
      if (bare.startsWith("/")) links.push({ label: pathLabel(bare), href: bare });
      continue;
    }
    const obj = asRecord(entry);
    if (!obj) continue;
    const href = pick(obj, LINK_URL_FIELDS);
    if (!href.startsWith("/")) continue;
    links.push({ label: pick(obj, LINK_LABEL_FIELDS) || pathLabel(href), href });
  }
  return links;
}

/** "/acme/issues/MUL-9" -> "MUL-9". Falls back to the whole path. */
function pathLabel(href: string): string {
  const last = href.split("?")[0]!.split("/").filter(Boolean).pop();
  return last || href;
}

function parseReceiptEffects(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const effects: string[] = [];
  for (const entry of value) {
    const bare = str(entry);
    if (bare) {
      effects.push(bare);
      continue;
    }
    const obj = asRecord(entry);
    const described = obj ? pick(obj, EFFECT_FIELDS) : "";
    if (described) effects.push(described);
  }
  return effects;
}

// --- uncertain outcomes -------------------------------------------------

export interface UncertainOutcome {
  /** What the user should go and look at. May be empty — the chip has a
   *  generic hint for that case. */
  inspect: string;
}

/** Decode `{"status":"uncertain","inspect":"…"}`. */
export function parseUncertainOutcome(result: unknown): UncertainOutcome | null {
  const obj = asRecord(result);
  if (!obj || str(obj.status) !== "uncertain") return null;
  return { inspect: pick(obj, ["inspect", "detail", "message"]) };
}

// --- outcome of a confirmation ------------------------------------------

export type OperationOutcome = "confirmed" | "rejected" | "expired" | "processing" | "failed" | "uncertain" | "unavailable";

/** Only a persisted execution outcome can claim success. */
export function operationState(operation: AssistantOperation): OperationOutcome | null {
  switch (operation.status) {
    case "pending": return null;
    case "rejected": return "rejected";
    case "expired": return "expired";
    case "uncertain": return "uncertain";
    case "confirmed":
      switch (operation.outcome) {
        case "succeeded": return "confirmed";
        case "failed": return "failed";
        case "uncertain": return "uncertain";
        default: return "processing";
      }
    default: return "unavailable";
  }
}

/**
 * Synthetic `tool_call_id` the server gives a receipt row so it can never
 * collide with a provider-generated call id — `op_<operation id>`. It is the
 * durable link from an outcome row back to the card, and the one that survives
 * a reload or a confirmation clicked on another device (see
 * server/internal/handler/assistant_operations.go, `assistantReceiptTag`).
 */
const RECEIPT_TOOL_CALL_PREFIX = "op_";

const REJECTED_STATUSES = new Set(["rejected", "cancelled", "canceled", "declined"]);
const EXPIRED_STATUSES = new Set(["expired", "stale", "superseded"]);

/**
 * Whether a LATER tool row already reported this operation's outcome — the
 * receipt the server persists after a confirm, or the "cancelled" row a reject
 * writes. Returns null when nothing references it yet, which keeps the card
 * pending (the only honest state: the click may have happened on another
 * device, and the transcript is the single source of truth for that).
 */
export function operationOutcomeAfter(
  messages: AssistantMessage[],
  index: number,
  operationId: string,
): OperationOutcome | null {
  const result = outcomeRowAfter(messages, index, operationId);
  if (!result) return null;
  const status = str(result.status).toLowerCase();
  if (REJECTED_STATUSES.has(status)) return "rejected";
  if (EXPIRED_STATUSES.has(status)) return "expired";
  if (status === "uncertain") return "uncertain";
  if (status === "failed" || status === "error") return "failed";
  if (asRecord(result.receipt)) return "confirmed";
  return "processing";
}

/**
 * Per-row outcomes of a confirmed PLAN, read from the very same later row
 * `operationOutcomeAfter` uses. That is what makes a plan receipt survive a
 * reload — and a confirmation clicked on another device — with no extra
 * channel: the server persists the execution receipt as a normal tool
 * message, and the card re-renders its per-row glyphs from it.
 *
 * Returns `[]` when nothing references the operation yet, and when the row
 * references it but carries no recognisable items (the card then shows the
 * settled state without glyphs, rather than a half-decoded checklist).
 */
export function planItemResultsAfter(
  messages: AssistantMessage[],
  index: number,
  operationId: string,
): PlanItemResult[] {
  const result = outcomeRowAfter(messages, index, operationId);
  if (!result) return [];
  const receipt = asRecord(result.receipt);
  const items = Array.isArray(result.items) ? result.items : receipt?.items;
  return parsePlanItemResults(items);
}

/** The first LATER tool row that reports this operation's outcome, if any. */
function outcomeRowAfter(
  messages: AssistantMessage[],
  index: number,
  operationId: string,
): Record<string, unknown> | null {
  if (!operationId) return null;
  for (let i = index + 1; i < messages.length; i++) {
    const message = messages[i];
    if (!message || message.role !== "tool") continue;
    const result = asRecord(message.tool_result);
    if (!result) continue;
    if (
      str(message.tool_call_id) !== RECEIPT_TOOL_CALL_PREFIX + operationId &&
      !referencesOperation(result, operationId)
    ) {
      continue;
    }
    // A repeat of the same pending card (a re-render of the tool row) is not
    // an outcome — it is the very thing being waited on.
    if (str(result.status).toLowerCase() === "needs_confirmation") continue;
    return result;
  }
  return null;
}

/** All the places a later row may carry the operation id it answers. */
function referencesOperation(result: Record<string, unknown>, operationId: string): boolean {
  if (str(result.operation_id) === operationId) return true;
  if (str(asRecord(result.operation)?.id) === operationId) return true;
  const receipt = asRecord(result.receipt);
  if (receipt && str(receipt.operation_id) === operationId) return true;
  return false;
}
