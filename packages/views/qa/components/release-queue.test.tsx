import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { ReleaseQueue } from "./release-queue";

// The Release page's Queue tab. These tests pin the empty-state contract the
// restructure introduced: the affirmative "review queue is clear — go to
// Ship" hint may ONLY render after a successful fetch. A failed request must
// show a neutral error state — never tell the QA team the loop is done.

const apiMocks = vi.hoisted(() => ({
  listIssues: vi.fn(),
  listQAVerdicts: vi.fn(),
  listProjects: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ issueDetail: (id: string) => `/w/issue/${id}` }),
}));
vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Someone" }),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));
// The live-run map has its own transitive query graph (task snapshot +
// squads) — stubbed; the strip's test covers the live-set semantics.
vi.mock("./qa-live-progress", () => ({
  useQaLiveIssueMap: () => ({ liveIssueIds: new Set<string>(), runningTaskByIssue: new Map<string, string>() }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a>,
}));

function renderQueue() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <ReleaseQueue onOpenShip={vi.fn()} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ReleaseQueue empty/error states", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.listQAVerdicts.mockResolvedValue({ verdicts: {} });
    apiMocks.listProjects.mockResolvedValue({ projects: [] });
  });

  it("shows the go-to-Ship empty state only on a successful empty fetch", async () => {
    apiMocks.listIssues.mockResolvedValue({ issues: [] });

    renderQueue();

    expect(await screen.findByText(/review queue is clear/)).toBeInTheDocument();
  });

  it("shows a neutral error state — NOT the all-clear — when the fetch fails", async () => {
    apiMocks.listIssues.mockRejectedValue(new Error("network down"));

    renderQueue();

    expect(await screen.findByText(/Couldn't load the review queue/)).toBeInTheDocument();
    expect(screen.queryByText(/review queue is clear/)).not.toBeInTheDocument();
  });

  it("renders the lanes, not the empty state, when the queue has issues", async () => {
    apiMocks.listIssues.mockResolvedValue({
      issues: [
        {
          id: "i-1",
          identifier: "MUL-1",
          title: "A failing task",
          priority: "high",
          labels: [{ id: "l1", name: "qa:fail", color: "" }],
        },
      ],
    });

    renderQueue();

    expect(await screen.findByText("A failing task")).toBeInTheDocument();
    expect(screen.queryByText(/review queue is clear/)).not.toBeInTheDocument();
  });
});
