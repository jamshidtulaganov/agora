// Built-in test-case templates (QA lens phase 3) — pure data, no react, no
// backend. The AddCaseForm's "Start from template" picker pre-fills the form
// from one of these; the user then edits every field before saving, so the
// SEED CONTENT stays English (it is authoring scaffolding, not UI chrome) while
// the picker LABELS are i18n'd view-side (test_cases.template_<id> keys).
//
// Angle-bracket placeholders (<entity>, <endpoint>) mark the parts the author
// must replace — the same convention the QA manifest docs use.

import type { ParsedStep } from "./steps";

export interface CaseTemplate {
  id: "login_flow" | "crud_happy_path" | "negative_validation" | "api_contract";
  title: string;
  preconditions: string;
  steps: ParsedStep[];
  expected: string;
  kind: "manual" | "automated";
  category: "positive" | "negative";
  priority: "p1" | "p2" | "p3";
  modality: "ui" | "api" | "unit" | "manual" | "";
}

export const CASE_TEMPLATES: readonly CaseTemplate[] = [
  {
    id: "login_flow",
    title: "[e2e] Login — happy path",
    preconditions: "A valid user account exists",
    steps: [
      { action: "Open the login page", expects: "Login form renders" },
      { action: "Enter valid credentials and submit" },
      { action: "Wait for the redirect", expects: "User lands on the dashboard, logged in" },
    ],
    expected: "User authenticates and reaches the dashboard",
    kind: "manual",
    category: "positive",
    priority: "p1",
    modality: "ui",
  },
  {
    id: "crud_happy_path",
    title: "[e2e] <entity> — create, edit, delete",
    preconditions: "Logged in with permission to manage <entity>",
    steps: [
      { action: "Create a new <entity> with valid data", expects: "It appears in the list" },
      { action: "Edit the <entity> and save", expects: "The change persists after reload" },
      { action: "Delete the <entity>", expects: "It disappears from the list" },
    ],
    expected: "Full create/edit/delete cycle works without errors",
    kind: "manual",
    category: "positive",
    priority: "p2",
    modality: "ui",
  },
  {
    id: "negative_validation",
    title: "[e2e] <form> — rejects invalid input",
    preconditions: "",
    steps: [
      { action: "Submit the form with required fields empty", expects: "Inline validation errors, nothing saved" },
      { action: "Enter out-of-range / wrong-type values and submit", expects: "Field errors shown, no crash" },
    ],
    expected: "Invalid input is rejected with clear errors; no partial writes",
    kind: "manual",
    category: "negative",
    priority: "p2",
    modality: "ui",
  },
  {
    id: "api_contract",
    title: "[api] <endpoint> — status + response shape",
    preconditions: "A valid auth token for the test workspace",
    steps: [
      { action: "Request the endpoint with valid auth", expects: "200 and the documented JSON shape" },
      { action: "Request it with missing/invalid auth", expects: "401/403, no data leaked" },
    ],
    expected: "Endpoint honors its contract for both authorized and unauthorized calls",
    kind: "manual",
    category: "positive",
    priority: "p2",
    modality: "api",
  },
] as const;
