import { z } from "zod";
import { parseWithFallback } from "../api/schema";
import type {
  Automation,
  AutomationCatalog,
  AutomationRun,
  InstallAutomationRecipeResponse,
  ListAutomationRecipesResponse,
  ListAutomationRunsResponse,
  ListAutomationsResponse,
} from "./types";

// Response schemas for the automations endpoints. Lenient on purpose (see
// CLAUDE.md "API Response Compatibility"): trigger/step/operator names stay
// `z.string()` so a server that adds one keeps rendering on an older client, and
// every list defaults to empty rather than failing the whole page.

const ConditionSchema = z.object({
  field: z.string().default(""),
  op: z.string().default(""),
  value: z.union([z.string(), z.array(z.string()), z.number(), z.boolean()]).optional(),
}).strip();

const StepSchema = z.object({
  type: z.string().default(""),
  config: z.record(z.string(), z.string()).optional(),
  conditions: z.array(ConditionSchema).optional(),
}).strip();

const AutomationSchema = z.object({
  id: z.string(),
  workspace_id: z.string().default(""),
  project_id: z.string().nullable().default(null),
  name: z.string().default(""),
  description: z.string().default(""),
  enabled: z.boolean().default(false),
  trigger_type: z.string().default(""),
  trigger_config: z.record(z.string(), z.unknown()).default({}),
  conditions: z.array(ConditionSchema).default([]),
  actions: z.array(StepSchema).default([]),
  recipe_key: z.string().default(""),
  run_count: z.number().default(0),
  last_run_at: z.string().nullable().default(null),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).strip();

const RunSchema = z.object({
  id: z.string(),
  automation_id: z.string().default(""),
  issue_id: z.string().nullable().default(null),
  trigger_type: z.string().default(""),
  status: z.string().default(""),
  actions_applied: z.number().default(0),
  detail: z.object({
    reason: z.string().optional(),
    retry_of: z.string().optional(),
    actor_type: z.string().optional(),
    actor_id: z.string().optional(),
    actions: z.array(z.object({
      type: z.string().default(""),
      ok: z.boolean().default(false),
      detail: z.string().optional(),
    }).strip()).optional(),
  }).strip().default({}),
  error: z.string().default(""),
  created_at: z.string().default(""),
}).strip();

const RecipeSchema = z.object({
  key: z.string(),
  title: z.string().default(""),
  description: z.string().default(""),
  category: z.string().default(""),
  flows: z.array(z.object({
    name: z.string().default(""),
    description: z.string().default(""),
    trigger_type: z.string().default(""),
    conditions: z.array(ConditionSchema).default([]),
    actions: z.array(StepSchema).default([]),
  }).strip()).default([]),
  installed: z.boolean().default(false),
}).strip();

const CatalogSchema = z.object({
  triggers: z.array(z.object({
    type: z.string().default(""),
    fields: z.array(z.string()).default([]),
  }).strip()).default([]),
  steps: z.array(z.string()).default([]),
  operators: z.array(z.string()).default([]),
  slice_action_kinds: z.array(z.string()).default([]),
  statuses: z.array(z.string()).default([]),
  assign_targets: z.array(z.string()).default([]),
  agent_selectors: z.array(z.string()).default([]),
  telegram_targets: z.array(z.string()).default([]),
  template_variables: z.array(z.string()).default([]),
  min_interval_default: z.number().default(30),
  max_per_hour_default: z.number().default(20),
}).strip();

const ListSchema = z.object({
  automations: z.array(AutomationSchema).default([]),
  total: z.number().default(0),
}).strip();

const RunsSchema = z.object({
  runs: z.array(RunSchema).default([]),
  total: z.number().default(0),
}).strip();

const RecipesSchema = z.object({
  recipes: z.array(RecipeSchema).default([]),
  total: z.number().default(0),
}).strip();

const InstallSchema = z.object({
  recipe: z.string().default(""),
  automations: z.array(AutomationSchema).default([]),
  enabled: z.boolean().default(false),
}).strip();

// An empty automation is the fallback for a single-object endpoint: the editor
// renders an empty flow instead of white-screening on a drifted response.
const EMPTY_AUTOMATION: Automation = {
  id: "",
  workspace_id: "",
  project_id: null,
  name: "",
  description: "",
  enabled: false,
  trigger_type: "",
  trigger_config: {},
  conditions: [],
  actions: [],
  recipe_key: "",
  run_count: 0,
  last_run_at: null,
  created_at: "",
  updated_at: "",
};

const EMPTY_CATALOG: AutomationCatalog = {
  triggers: [],
  steps: [],
  operators: [],
  slice_action_kinds: [],
  statuses: [],
  assign_targets: [],
  agent_selectors: [],
  telegram_targets: [],
  template_variables: [],
  min_interval_default: 30,
  max_per_hour_default: 20,
};

export function parseAutomationsResponse(value: unknown): ListAutomationsResponse {
  return parseWithFallback(value, ListSchema, { automations: [], total: 0 }, {
    endpoint: "GET /api/automations",
  });
}

export function parseAutomationResponse(value: unknown): Automation {
  return parseWithFallback(value, AutomationSchema, EMPTY_AUTOMATION, {
    endpoint: "GET /api/automations/{id}",
  });
}

export function parseAutomationRunsResponse(value: unknown): ListAutomationRunsResponse {
  return parseWithFallback(value, RunsSchema, { runs: [], total: 0 }, {
    endpoint: "GET /api/automations/{id}/runs",
  });
}

export function parseAutomationRunResponse(value: unknown): AutomationRun {
  return parseWithFallback(value, RunSchema, {
    id: "",
    automation_id: "",
    issue_id: null,
    trigger_type: "",
    status: "failed",
    actions_applied: 0,
    detail: {},
    error: "",
    created_at: "",
  }, {
    endpoint: "POST /api/automations/{id}/runs/{runId}/rerun",
  });
}

export function parseAutomationRecipesResponse(value: unknown): ListAutomationRecipesResponse {
  return parseWithFallback(value, RecipesSchema, { recipes: [], total: 0 }, {
    endpoint: "GET /api/automations/recipes",
  });
}

export function parseAutomationCatalogResponse(value: unknown): AutomationCatalog {
  return parseWithFallback(value, CatalogSchema, EMPTY_CATALOG, {
    endpoint: "GET /api/automations/catalog",
  });
}

export function parseInstallRecipeResponse(value: unknown): InstallAutomationRecipeResponse {
  return parseWithFallback(value, InstallSchema, { recipe: "", automations: [], enabled: false }, {
    endpoint: "POST /api/automations/recipes/{key}/install",
  });
}
