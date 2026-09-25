import { describe, expect, it, vi } from "vitest";
import type { AgentRuntime, AgentTemplateSummary } from "@agora/core/types";
import { createSetupAgents, pickSetupRuntime, setupTemplates } from "./setup-agents";

function runtime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt",
    owner_id: "user-1",
    visibility: "private",
    status: "online",
    ...overrides,
  } as AgentRuntime;
}

function template(slug: string, category = "Business"): AgentTemplateSummary {
  return { slug, name: slug, description: "", category, skills: [] };
}

const DIGEST = {
  title: "Weekly digest",
  description: "Write the digest.",
  issueTitle: "Weekly digest",
  scheduleLabel: "Every Monday at 9:00",
};

describe("setupTemplates", () => {
  it("keeps only the Business category", () => {
    expect(
      setupTemplates([template("a"), template("b", "Engineering"), template("c")]).map((t) => t.slug),
    ).toEqual(["a", "c"]);
  });
});

describe("pickSetupRuntime", () => {
  it("prefers an online runtime the person can use", () => {
    expect(
      pickSetupRuntime(
        [runtime({ id: "off", status: "offline" }), runtime({ id: "on", status: "online" })],
        "user-1",
      )?.id,
    ).toBe("on");
  });

  it("falls back to an offline one", () => {
    expect(pickSetupRuntime([runtime({ id: "off", status: "offline" })], "user-1")?.id).toBe("off");
  });

  it("skips someone else's private runtime but takes a public one", () => {
    expect(
      pickSetupRuntime(
        [
          runtime({ id: "theirs", owner_id: "other", visibility: "private" }),
          runtime({ id: "shared", owner_id: "other", visibility: "public", status: "offline" }),
        ],
        "user-1",
      )?.id,
    ).toBe("shared");
  });

  it("returns null when there is nothing to use", () => {
    expect(pickSetupRuntime([], "user-1")).toBeNull();
    expect(pickSetupRuntime([runtime({ owner_id: "other" })], "user-1")).toBeNull();
  });
});

describe("createSetupAgents", () => {
  function makeApi() {
    return {
      createAgentFromTemplate: vi.fn(async ({ template_slug }: { template_slug: string }) => ({
        agent: { id: `agent-${template_slug}` },
        imported_skill_ids: [],
        reused_skill_ids: [],
      })),
      createAutopilot: vi.fn(async () => ({ id: "ap-1" })),
      createAutopilotTrigger: vi.fn(async () => ({ id: "tr-1" })),
    };
  }

  it("creates each agent and a Monday autopilot for the weekly digest", async () => {
    const api = makeApi();
    const results = await createSetupAgents(api as never, {
      templates: [template("department-assistant"), template("weekly-digest")],
      runtimeId: "rt-1",
      timezone: "Asia/Tashkent",
      digest: DIGEST,
    });

    expect(results).toEqual([
      { slug: "department-assistant", name: "department-assistant", status: "added" },
      { slug: "weekly-digest", name: "weekly-digest", status: "added", schedule: "added" },
    ]);
    expect(api.createAutopilot).toHaveBeenCalledTimes(1);
    expect(api.createAutopilotTrigger).toHaveBeenCalledWith("ap-1", {
      kind: "schedule",
      cron_expression: "0 9 * * 1",
      timezone: "Asia/Tashkent",
      label: "Every Monday at 9:00",
    });
  });

  it("keeps going after a failure", async () => {
    const api = makeApi();
    api.createAgentFromTemplate.mockRejectedValueOnce(new Error("name taken"));
    const results = await createSetupAgents(api as never, {
      templates: [template("intake-triager"), template("department-assistant")],
      runtimeId: "rt-1",
      timezone: "UTC",
      digest: DIGEST,
    });
    expect(results.map((r) => r.status)).toEqual(["failed", "added"]);
    expect(results[0]?.error).toBe("name taken");
  });

  it("reports a missing schedule when the create response drifted", async () => {
    const api = makeApi();
    api.createAgentFromTemplate.mockResolvedValueOnce({
      agent: { id: "" },
      imported_skill_ids: [],
      reused_skill_ids: [],
    });
    const [result] = await createSetupAgents(api as never, {
      templates: [template("weekly-digest")],
      runtimeId: "rt-1",
      timezone: "UTC",
      digest: DIGEST,
    });
    expect(result).toMatchObject({ status: "added", schedule: "failed" });
    expect(api.createAutopilot).not.toHaveBeenCalled();
  });

  it("reports a missing schedule when the autopilot has no id", async () => {
    const api = makeApi();
    api.createAutopilot.mockResolvedValueOnce({} as { id: string });
    const [result] = await createSetupAgents(api as never, {
      templates: [template("weekly-digest")],
      runtimeId: "rt-1",
      timezone: "UTC",
      digest: DIGEST,
    });
    expect(result).toMatchObject({ status: "added", schedule: "failed" });
    expect(api.createAutopilotTrigger).not.toHaveBeenCalled();
  });
});
