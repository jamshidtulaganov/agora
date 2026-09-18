import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { PinnedReport, PinnedReportSummary } from "@agora/core/types";
import { RESOURCES } from "../../locales";

const mockListReports = vi.hoisted(() => vi.fn());
const mockGetReport = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/reports", () => ({
  projectReportsOptions: (wsId: string, projectId: string) => ({
    queryKey: ["reports", wsId, "project", projectId],
    queryFn: () => mockListReports(projectId),
    retry: false,
  }),
  reportOptions: (wsId: string, pinId: string) => ({
    queryKey: ["reports", wsId, "detail", pinId],
    queryFn: () => mockGetReport(pinId),
    retry: false,
  }),
}));

import { ProjectReportsSection } from "./project-reports-section";

function report(overrides: Partial<PinnedReportSummary> = {}): PinnedReportSummary {
  return {
    pin_id: "pin-1",
    artifact_id: "art-1",
    title: "Sprint report",
    kind: "markdown",
    version: 3,
    updated_at: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    created_at: "2026-09-17T10:00:00Z",
    pinned_by: { id: "u-1", name: "Jamshid" },
    owner: { id: "u-1", name: "Jamshid" },
    ...overrides,
  };
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <ProjectReportsSection projectId="proj-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("ProjectReportsSection", () => {
  it("lists pinned reports with version, freshness and owner", async () => {
    mockListReports.mockResolvedValue([
      report(),
      report({ pin_id: "pin-2", title: "QA health", kind: "table", version: 1 }),
    ]);

    renderSection();

    expect(await screen.findByText("Reports")).toBeInTheDocument();
    expect(screen.getByText("Sprint report")).toBeInTheDocument();
    expect(screen.getByText(/v3 · updated 2h ago/)).toBeInTheDocument();
    expect(screen.getAllByText(/by Jamshid/)).toHaveLength(2);
    expect(screen.getByText("QA health")).toBeInTheDocument();
  });

  it("renders nothing at all when no report is pinned (no empty placeholder)", async () => {
    mockListReports.mockResolvedValue([]);
    const { container } = renderSection();
    await vi.waitFor(() => expect(mockListReports).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the endpoint is not deployed yet (404)", async () => {
    mockListReports.mockRejectedValue(new Error("404"));
    const { container } = renderSection();
    await vi.waitFor(() => expect(mockListReports).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("drops a row whose pin id drifted away rather than rendering a dead target", async () => {
    mockListReports.mockResolvedValue([report({ pin_id: "" }), report({ pin_id: "pin-2", title: "QA health" })]);
    renderSection();
    expect(await screen.findByText("QA health")).toBeInTheDocument();
    expect(screen.queryByText("Sprint report")).not.toBeInTheDocument();
  });

  it("opens a read-only viewer that renders the artifact body", async () => {
    mockListReports.mockResolvedValue([report()]);
    const detail: PinnedReport = { ...report(), content: "## Week 12\n\nShipped the pin flow." };
    mockGetReport.mockResolvedValue(detail);

    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "Open report: Sprint report" }));

    expect(await screen.findByText("Shipped the pin flow.")).toBeInTheDocument();
    expect(mockGetReport).toHaveBeenCalledWith("pin-1");
    // Read-only: none of the pane's owner affordances leak into the viewer.
    expect(screen.queryByRole("button", { name: /Edit/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Version history/ })).not.toBeInTheDocument();
  });

  it("degrades an unreadable report body to a quiet notice", async () => {
    mockListReports.mockResolvedValue([report()]);
    // EMPTY_PINNED_REPORT: a 200 whose body failed schema validation.
    mockGetReport.mockResolvedValue({ ...report({ pin_id: "" }), title: "", content: "" });

    renderSection();
    fireEvent.click(await screen.findByRole("button", { name: "Open report: Sprint report" }));

    expect(await screen.findByText("This report isn't available.")).toBeInTheDocument();
  });
});
