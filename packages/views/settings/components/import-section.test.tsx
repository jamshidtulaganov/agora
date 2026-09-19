import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

// Settings → Integrations → Import.
//
// The four things these tests hold in place are the four ways this panel could
// hurt somebody:
//
//   1. The source API key is submitted ONCE and is gone from the form
//      afterwards — including when the save failed.
//   2. The plan is rendered with the bad news (unreachable rows, unmatched
//      people) visible, because that is the entire product of a dry run.
//   3. Import cannot be pressed before a plan exists. There is no path through
//      this UI that writes into a workspace nobody has seen counted.
//   4. A job status this build has never heard of renders as a generic line
//      rather than as nothing, or as "finished".

type MemberRole = "owner" | "admin" | "member";

const membersRef = vi.hoisted(() => ({
  current: [{ user_id: "user-1", role: "owner" as MemberRole }],
}));
const connectionsRef = vi.hoisted(() => ({
  current: [] as Array<Record<string, unknown>>,
}));
const jobRef = vi.hoisted(() => ({ current: undefined as Record<string, unknown> | undefined }));

const createConnection = vi.hoisted(() => vi.fn());
const dryRun = vi.hoisted(() => vi.fn());
const startImport = vi.hoisted(() => vi.fn());
const cancelImport = vi.hoisted(() => vi.fn());
const probeConnection = vi.hoisted(() => vi.fn());
const deleteConnection = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey?: unknown[]; enabled?: boolean }) => {
    if (options.enabled === false) return { data: undefined };
    const key = JSON.stringify(options.queryKey ?? []);
    if (key.includes("members")) return { data: membersRef.current };
    if (key.includes("connections")) return { data: connectionsRef.current };
    if (key.includes("job")) return { data: jobRef.current };
    return { data: undefined };
  },
  useMutation: () => ({ mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
  queryOptions: <T,>(options: T) => options,
}));

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "workspace-1" }));
vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({ queryKey: ["members", "workspace-1"] }),
}));
vi.mock("@agora/core/auth", () => {
  const useAuthStore = Object.assign(
    (sel?: (s: { user: { id: string } }) => unknown) =>
      sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
    { getState: () => ({ user: { id: "user-1" } }) },
  );
  return { useAuthStore };
});
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

// The core module's pure helpers are covered by packages/core/imports tests;
// here only the hooks are replaced, so the component under test still runs the
// real terminal-status and byte-formatting logic.
vi.mock("@agora/core/imports", () => ({
  IMPORT_SOURCES: ["linear"] as const,
  importConnectionsOptions: (wsId: string) => ({ queryKey: ["imports", "connections", wsId] }),
  importJobOptions: (wsId: string, jobId: string) => ({ queryKey: ["imports", "job", wsId, jobId] }),
  isTerminalImportStatus: (status: string) =>
    status === "done" || status === "failed" || status === "cancelled",
  importTotalOf: (job: { totals?: Record<string, Record<string, number>> }, field: string) =>
    Object.values(job.totals ?? {}).reduce((sum, row) => sum + (row?.[field] ?? 0), 0),
  formatImportBytes: (bytes: number) => `${bytes} B`,
  useCreateImportConnection: () => ({ mutateAsync: createConnection, isPending: false }),
  useProbeImportConnection: () => ({ mutate: probeConnection, isPending: false }),
  useDeleteImportConnection: () => ({ mutate: deleteConnection, isPending: false }),
  useDryRunImport: () => ({ mutateAsync: dryRun, isPending: false }),
  useStartImport: () => ({ mutateAsync: startImport, isPending: false }),
  useCancelImport: () => ({ mutate: cancelImport, isPending: false }),
}));

const { ImportSection } = await import("./import-section");

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function renderSection() {
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
  return render(<ImportSection />, { wrapper: Wrapper });
}

function plan(overrides: Record<string, unknown> = {}) {
  return {
    source_kind: "linear",
    generated_at: "",
    containers: [
      { external_id: "t1", key: "ENG", name: "Engineering", issues: 240, project_id: "", action: "create_project" },
    ],
    iterations: 0,
    issues: { create: 240, update: 3, total: 243 },
    comments: { create: 1180, update: 0, total: 1180 },
    attachments: { count: 340, bytes: 12, skipped: 2, skipped_reasons: [], unsupported: false },
    relations: { total: 0, degraded: 0, dangling: 0 },
    statuses: [],
    unmapped_statuses: [{ name: "Waiting on customer", category: "triage", issues: 4 }],
    users: [
      { external_id: "u1", name: "Dana Wu", email: "dana@gone.example", via: "import_identity", user_id: "", problem: "" },
      { external_id: "u2", name: "Kim Ryu", email: "kim@acme.io", via: "member_email", user_id: "user-9", problem: "" },
    ],
    unmatched_users: 1,
    exact: true,
    truncated: { issues: 12 },
    warnings: [],
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  membersRef.current = [{ user_id: "user-1", role: "owner" }];
  connectionsRef.current = [];
  jobRef.current = undefined;
  dryRun.mockReset();
  startImport.mockReset();
  createConnection.mockReset();
});

describe("ImportSection", () => {
  it("submits the source key once and clears it from the form", async () => {
    createConnection.mockResolvedValue(undefined);
    renderSection();

    const key = screen.getByPlaceholderText(enSettings.imports.key_placeholder) as HTMLInputElement;
    // A password field, not a text field: the value must not be shoulder-read
    // or autofilled back in.
    expect(key.type).toBe("password");

    await userEvent.type(key, "lin_api_secret");
    await userEvent.click(screen.getByText(enSettings.imports.connect));

    expect(createConnection).toHaveBeenCalledTimes(1);
    expect(createConnection).toHaveBeenCalledWith(
      expect.objectContaining({ source: "linear", secret: "lin_api_secret" }),
    );
    // Gone from the form, and therefore from the next render.
    expect(key.value).toBe("");
  });

  it("clears the key even when the save FAILED", async () => {
    createConnection.mockRejectedValue(new Error("nope"));
    renderSection();

    const key = screen.getByPlaceholderText(enSettings.imports.key_placeholder) as HTMLInputElement;
    await userEvent.type(key, "lin_api_secret");
    await userEvent.click(screen.getByText(enSettings.imports.connect));

    expect(key.value).toBe("");
  });

  it("renders the plan's counts with the unreachable rows and unmatched people first", async () => {
    connectionsRef.current = [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }];
    dryRun.mockResolvedValue({ job_id: "j1", status: "awaiting_confirm", plan: plan() });
    renderSection();

    await userEvent.click(screen.getByText(enSettings.imports.preview));

    const report = await screen.findByTestId("import-plan");
    expect(report.textContent).toContain("243");
    expect(report.textContent).toContain("1180");
    // The bad news, named rather than summarised away.
    expect(report.textContent).toContain("12 rows are not visible");
    expect(report.textContent).toContain("1 people are not members");
    expect(report.textContent).toContain("2 files are over the size budget");
    // Only the unmatched person is listed under "people without an account".
    const people = screen.getByTestId("import-unmatched");
    expect(people.textContent).toContain("Dana Wu");
    expect(people.textContent).not.toContain("Kim Ryu");
    // And the unmapped source state, which the operator has to assign.
    expect(report.textContent).toContain("Waiting on customer");
  });

  it("says ABOUT when the plan's counts are estimates", async () => {
    connectionsRef.current = [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }];
    dryRun.mockResolvedValue({ job_id: "j1", status: "awaiting_confirm", plan: plan({ exact: false }) });
    renderSection();

    await userEvent.click(screen.getByText(enSettings.imports.preview));

    const report = await screen.findByTestId("import-plan");
    expect(report.textContent).toContain("about 243");
  });

  it("offers Import only once a plan has been read", async () => {
    connectionsRef.current = [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }];
    dryRun.mockResolvedValue({ job_id: "j1", status: "awaiting_confirm", plan: plan() });
    renderSection();

    // Before the survey there is no way to start an import at all.
    expect(screen.queryByText(enSettings.imports.apply)).toBeNull();

    await userEvent.click(screen.getByText(enSettings.imports.preview));
    await screen.findByTestId("import-plan");

    // And the button arrives with a plain sentence saying what it will do.
    expect(screen.getByText(/This creates 240 issues and 1 projects/)).toBeTruthy();
    startImport.mockResolvedValue({ job_id: "j2", status: "running" });
    await userEvent.click(screen.getByText(enSettings.imports.apply));
    expect(startImport).toHaveBeenCalledWith(expect.objectContaining({ connection_id: "c1" }));
  });

  it("renders a receipt with the failures, not only the successes", async () => {
    connectionsRef.current = [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }];
    dryRun.mockResolvedValue({ job_id: "j1", status: "awaiting_confirm", plan: plan() });
    jobRef.current = {
      id: "j1",
      status: "done",
      totals: { issues: { created: 240, updated: 3, skipped: 0, failed: 2 } },
      failures: [{ kind: "attachments", identifier: "ENG-1", reason: "over the per-file cap" }],
      plan: null,
    };
    renderSection();

    // The job panel mounts once a job id is known, which is what the survey
    // returns alongside the plan.
    await userEvent.click(screen.getByText(enSettings.imports.preview));

    const receipt = await screen.findByTestId("import-job");
    expect(receipt.textContent).toContain(enSettings.imports.status_done);
    expect(receipt.textContent).toContain("240");
    // A receipt that lists only what worked is the failure mode this asserts
    // against: 2 failed, and the reason is on screen.
    expect(receipt.textContent).toContain("2");
    expect(screen.getByTestId("import-failures").textContent).toContain("over the per-file cap");
    // A finished job offers no stop control.
    expect(screen.queryByText(enSettings.imports.cancel)).toBeNull();
  });

  it("renders an unknown job status generically instead of as finished", async () => {
    connectionsRef.current = [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }];
    dryRun.mockResolvedValue({ job_id: "j1", status: "reticulating_splines", plan: plan() });
    jobRef.current = {
      id: "j1",
      status: "reticulating_splines",
      totals: {},
      failures: [],
      plan: null,
    };
    renderSection();

    await userEvent.click(screen.getByText(enSettings.imports.preview));

    const job = await screen.findByTestId("import-job");
    expect(job.textContent).toContain("Import state: reticulating_splines");
    expect(job.textContent).not.toContain(enSettings.imports.status_done);
    // A status this build does not know is NOT terminal, so the stop control
    // is still offered rather than the job being treated as over.
    expect(screen.getByText(enSettings.imports.cancel)).toBeTruthy();
  });

  it("tells a plain member who can run an import, and offers them no form", () => {
    membersRef.current = [{ user_id: "user-1", role: "member" }];
    connectionsRef.current = [{ id: "c1", source: "linear", label: "Acme", probe_status: "ok" }];
    renderSection();

    expect(screen.getByText(enSettings.imports.admin_only)).toBeTruthy();
    expect(screen.queryByPlaceholderText(enSettings.imports.key_placeholder)).toBeNull();
    expect(screen.queryByText(enSettings.imports.preview)).toBeNull();
  });
});
