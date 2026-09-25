import type { ApiClient } from "@agora/core/api";
import type { AgentRuntime, AgentTemplateSummary } from "@agora/core/types";

/** Ready-made agents the setup offers: the "Business" template category. */
export const SETUP_TEMPLATE_CATEGORY = "Business";

/** The template that also gets a weekly autopilot. */
export const WEEKLY_DIGEST_SLUG = "weekly-digest";

/** Monday 09:00 in the chosen timezone. */
export const WEEKLY_DIGEST_CRON = "0 9 * * 1";

export function setupTemplates(templates: readonly AgentTemplateSummary[]): AgentTemplateSummary[] {
  return templates.filter((template) => template.category === SETUP_TEMPLATE_CATEGORY);
}

/**
 * The runtime new setup agents run on: one the person may use (theirs, or a
 * public one — the same rule as the create-agent runtime picker), preferring
 * one that is online right now. Null when there is none to use.
 */
export function pickSetupRuntime(
  runtimes: readonly AgentRuntime[],
  userId: string | null,
): AgentRuntime | null {
  const usable = runtimes.filter(
    (runtime) => !userId || runtime.owner_id === userId || runtime.visibility === "public",
  );
  return usable.find((runtime) => runtime.status === "online") ?? usable[0] ?? null;
}

export interface SetupAgentResult {
  slug: string;
  name: string;
  status: "added" | "failed";
  error?: string;
  /** Only for the weekly digest: whether its Monday autopilot was set up. */
  schedule?: "added" | "failed";
  scheduleError?: string;
}

export interface CreateSetupAgentsInput {
  templates: readonly AgentTemplateSummary[];
  runtimeId: string;
  timezone: string;
  /** Localized copy for the weekly digest autopilot. */
  digest: {
    title: string;
    description: string;
    issueTitle: string;
    scheduleLabel: string;
  };
}

type SetupApi = Pick<ApiClient, "createAgentFromTemplate" | "createAutopilot" | "createAutopilotTrigger">;

function errorText(err: unknown): string | undefined {
  return err instanceof Error && err.message ? err.message : undefined;
}

/**
 * Creates the chosen ready-made agents one after another. Every template gets
 * its own result, and a failure never stops the ones after it — the person
 * can always finish the setup and fix a single agent later.
 *
 * The weekly digest also gets an autopilot that opens an issue every Monday
 * at 09:00 in the person's timezone: a digest is only useful if the team can
 * read it, and a run-only autopilot leaves nothing in Issues or the Inbox.
 */
export async function createSetupAgents(
  api: SetupApi,
  input: CreateSetupAgentsInput,
): Promise<SetupAgentResult[]> {
  const results: SetupAgentResult[] = [];
  for (const template of input.templates) {
    let agentId = "";
    try {
      const created = await api.createAgentFromTemplate({
        template_slug: template.slug,
        name: template.name,
        runtime_id: input.runtimeId,
      });
      agentId = typeof created?.agent?.id === "string" ? created.agent.id : "";
    } catch (err) {
      results.push({ slug: template.slug, name: template.name, status: "failed", error: errorText(err) });
      continue;
    }

    const result: SetupAgentResult = { slug: template.slug, name: template.name, status: "added" };
    if (template.slug === WEEKLY_DIGEST_SLUG) {
      try {
        // A drifted create response has no agent id to assign the autopilot to.
        if (!agentId) throw new Error();
        const autopilot = await api.createAutopilot({
          title: input.digest.title,
          description: input.digest.description,
          assignee_type: "agent",
          assignee_id: agentId,
          execution_mode: "create_issue",
          // `{{date}}` is filled in by the server on every run.
          issue_title_template: `${input.digest.issueTitle} {{date}}`,
        });
        const autopilotId = typeof autopilot?.id === "string" ? autopilot.id : "";
        if (!autopilotId) throw new Error();
        await api.createAutopilotTrigger(autopilotId, {
          kind: "schedule",
          cron_expression: WEEKLY_DIGEST_CRON,
          timezone: input.timezone,
          label: input.digest.scheduleLabel,
        });
        result.schedule = "added";
      } catch (err) {
        result.schedule = "failed";
        result.scheduleError = errorText(err);
      }
    }
    results.push(result);
  }
  return results;
}
