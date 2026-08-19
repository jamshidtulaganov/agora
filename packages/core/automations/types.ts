// Automations: user-defined task-management flows — WHEN a trigger fires, IF the
// conditions hold, THEN run the steps. The shapes here mirror the server contract
// (server/internal/handler/automation_types.go); the catalog endpoint is the source
// of truth for which trigger/step/operator names are live, so nothing here is a
// hardcoded union that would go stale on a server upgrade.

/** A trigger's identifier, e.g. "issue.status_changed". Kept as a string so a
 *  server that adds a trigger does not need a client release. */
export type AutomationTrigger = string;

/** One condition clause. All clauses on a node must hold (AND). */
export interface AutomationCondition {
  field: string;
  op: string;
  value?: string | string[];
}

/** One flow step. `filter` uses `conditions` and stops the flow when they fail;
 *  every other type uses `config`. */
export interface AutomationStep {
  type: string;
  config?: Record<string, string>;
  conditions?: AutomationCondition[];
}

export interface Automation {
  id: string;
  workspace_id: string;
  project_id: string | null;
  name: string;
  description: string;
  enabled: boolean;
  trigger_type: AutomationTrigger;
  trigger_config: Record<string, unknown>;
  conditions: AutomationCondition[];
  actions: AutomationStep[];
  recipe_key: string;
  run_count: number;
  last_run_at: string | null;
  created_at: string;
  updated_at: string;
}

/** One evaluation of one automation. `skipped` rows carry the reason in
 *  `detail.reason` — they are the answer to "why did my flow not fire". */
export interface AutomationRun {
  id: string;
  automation_id: string;
  issue_id: string | null;
  trigger_type: string;
  status: string;
  actions_applied: number;
  detail: AutomationRunDetail;
  error: string;
  created_at: string;
}

export interface AutomationRunDetail {
  reason?: string;
  actions?: Array<{ type: string; ok: boolean; detail?: string }>;
}

/** What one trigger carries, so the condition picker only offers facts that exist
 *  for the chosen trigger. */
export interface AutomationTriggerInfo {
  type: string;
  fields: string[];
}

/** The node palette the flow editor renders, served from the same registries the
 *  engine evaluates — so the editor can never offer something inert. */
export interface AutomationCatalog {
  triggers: AutomationTriggerInfo[];
  steps: string[];
  operators: string[];
  slice_action_kinds: string[];
  statuses: string[];
  assign_targets: string[];
  agent_selectors: string[];
  telegram_targets: string[];
  template_variables: string[];
  min_interval_default: number;
  max_per_hour_default: number;
}

export interface AutomationRecipeFlow {
  name: string;
  description: string;
  trigger_type: string;
  conditions: AutomationCondition[];
  actions: AutomationStep[];
}

export interface AutomationRecipe {
  key: string;
  title: string;
  description: string;
  category: string;
  flows: AutomationRecipeFlow[];
  installed: boolean;
}

export interface AutomationWriteRequest {
  name: string;
  description?: string;
  enabled?: boolean;
  project_id?: string | null;
  trigger_type: string;
  trigger_config?: Record<string, unknown>;
  conditions: AutomationCondition[];
  actions: AutomationStep[];
}

export interface ListAutomationsResponse {
  automations: Automation[];
  total: number;
}

export interface ListAutomationRunsResponse {
  runs: AutomationRun[];
  total: number;
}

export interface ListAutomationRecipesResponse {
  recipes: AutomationRecipe[];
  total: number;
}

export interface InstallAutomationRecipeResponse {
  recipe: string;
  automations: Automation[];
  enabled: boolean;
}
