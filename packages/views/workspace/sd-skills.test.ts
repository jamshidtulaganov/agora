import { beforeEach, describe, expect, it, vi } from "vitest";

const mockListSkills = vi.fn();
const mockCreateSkill = vi.fn();
const mockAddAgentSkills = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    listSkills: (...args: unknown[]) => mockListSkills(...args),
    createSkill: (...args: unknown[]) => mockCreateSkill(...args),
    addAgentSkills: (...args: unknown[]) => mockAddAgentSkills(...args),
  },
}));

// Imported AFTER the mock so the module-level `api` binding resolves to the
// mocked client. `vi.resetModules` in beforeEach gives each test a fresh
// module instance — critical for the once-per-workspace guard, which is
// module-level state.
let SD_SKILLS: typeof import("./sd-skills").SD_SKILLS;
let seedSdSkills: typeof import("./sd-skills").seedSdSkills;

beforeEach(async () => {
  vi.resetModules();
  mockListSkills.mockReset();
  mockCreateSkill.mockReset();
  mockAddAgentSkills.mockReset();
  // Re-import to get a module with a fresh `pendingSeed` guard map.
  const mod = await import("./sd-skills");
  SD_SKILLS = mod.SD_SKILLS;
  seedSdSkills = mod.seedSdSkills;
});

function skillSummary(name: string, id: string) {
  return {
    id,
    workspace_id: "ws-1",
    name,
    description: "",
    config: {},
    created_by: null,
    created_at: "",
    updated_at: "",
  };
}

describe("SD_SKILLS", () => {
  it("includes the four SD starter skills incl. the qa-process flow", () => {
    const names = SD_SKILLS.map((s) => s.name);
    expect(names).toEqual([
      "sd-architecture",
      "sd-coding-standards",
      "sd-review-checklist",
      "sd-qa-process",
    ]);
  });

  it("sd-qa-process body carries the QA_* env block and the qa_switch hook", () => {
    const qa = SD_SKILLS.find((s) => s.name === "sd-qa-process");
    expect(qa).toBeDefined();
    const body = qa!.content;
    expect(body).toContain("QA_SWITCH_URL=https://<name>.sddev.uz/qa_switch.php");
    expect(body).toContain("QA_SWITCH_TOKEN=");
    expect(body).toContain("QA_SDDEV_URL=");
    expect(body).toContain("QA_SDDEV_BASE_BRANCH=billing");
    expect(body).toContain("QA_LOGIN=");
    expect(body).toContain("QA_PASSWORD=");
    expect(body).toContain("QA_LOGIN_PATH=/site/login");
    expect(body).toContain("qa_switch.php");
    expect(body).toContain("btx-");
  });
});

describe("seedSdSkills", () => {
  it("creates every missing skill then attaches all of them to the agent", async () => {
    // No skills exist yet → all four get created.
    mockListSkills.mockResolvedValue([]);
    mockCreateSkill.mockImplementation((data: { name: string }) =>
      Promise.resolve({ id: `id-${data.name}`, name: data.name }),
    );
    mockAddAgentSkills.mockResolvedValue(undefined);

    await seedSdSkills("ws-1", "agent-1");

    // One create per SD skill, in declared order.
    expect(mockCreateSkill).toHaveBeenCalledTimes(SD_SKILLS.length);
    const createdNames = mockCreateSkill.mock.calls.map(([d]) => d.name);
    expect(createdNames).toEqual(SD_SKILLS.map((s) => s.name));

    // Attach all created ids in one idempotent call.
    expect(mockAddAgentSkills).toHaveBeenCalledTimes(1);
    const [agentId, skillIds] = mockAddAgentSkills.mock.calls[0]!;
    expect(agentId).toBe("agent-1");
    expect(skillIds).toEqual(SD_SKILLS.map((s) => `id-${s.name}`));
  });

  it("reuses skills that already exist by name instead of recreating them", async () => {
    // Two of the four already exist in the workspace.
    mockListSkills.mockResolvedValue([
      skillSummary("sd-architecture", "existing-arch"),
      skillSummary("sd-review-checklist", "existing-review"),
    ]);
    mockCreateSkill.mockImplementation((data: { name: string }) =>
      Promise.resolve({ id: `id-${data.name}`, name: data.name }),
    );
    mockAddAgentSkills.mockResolvedValue(undefined);

    await seedSdSkills("ws-1", "agent-1");

    // Only the missing two are created.
    const createdNames = mockCreateSkill.mock.calls.map(([d]) => d.name);
    expect(createdNames).toEqual(["sd-coding-standards", "sd-qa-process"]);

    // Attach reuses the existing ids and adds the new ones — all four, in
    // SD_SKILLS order.
    const [, skillIds] = mockAddAgentSkills.mock.calls[0]!;
    expect(skillIds).toEqual([
      "existing-arch",
      "id-sd-coding-standards",
      "existing-review",
      "id-sd-qa-process",
    ]);
  });

  it("seeds at most once per workspace per session", async () => {
    mockListSkills.mockResolvedValue([]);
    mockCreateSkill.mockImplementation((data: { name: string }) =>
      Promise.resolve({ id: `id-${data.name}`, name: data.name }),
    );
    mockAddAgentSkills.mockResolvedValue(undefined);

    await seedSdSkills("ws-1", "agent-1");
    await seedSdSkills("ws-1", "agent-1");

    // Second call is a no-op: listSkills / addAgentSkills fire exactly once.
    expect(mockListSkills).toHaveBeenCalledTimes(1);
    expect(mockAddAgentSkills).toHaveBeenCalledTimes(1);
  });

  it("is best-effort: swallows errors and releases the guard so a retry can run", async () => {
    // First attempt fails on listSkills.
    mockListSkills.mockRejectedValueOnce(new Error("network down"));

    // Must not throw.
    await expect(seedSdSkills("ws-1", "agent-1")).resolves.toBeUndefined();
    expect(mockAddAgentSkills).not.toHaveBeenCalled();

    // Guard released on failure → a retry runs a fresh pass and succeeds.
    mockListSkills.mockResolvedValue([]);
    mockCreateSkill.mockImplementation((data: { name: string }) =>
      Promise.resolve({ id: `id-${data.name}`, name: data.name }),
    );
    mockAddAgentSkills.mockResolvedValue(undefined);

    await seedSdSkills("ws-1", "agent-1");

    expect(mockListSkills).toHaveBeenCalledTimes(2);
    expect(mockAddAgentSkills).toHaveBeenCalledTimes(1);
  });

  it("no-ops when agentId is missing", async () => {
    await seedSdSkills("ws-1", "");
    expect(mockListSkills).not.toHaveBeenCalled();
  });
});
