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
  };
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

export type OperationOutcome = "confirmed" | "rejected" | "expired";

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
    const status = str(result.status).toLowerCase();
    if (status === "needs_confirmation") continue;
    if (REJECTED_STATUSES.has(status)) return "rejected";
    if (EXPIRED_STATUSES.has(status)) return "expired";
    return "confirmed";
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
