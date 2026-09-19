import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { StaleIssue } from "@agora/core/types";
import { RESOURCES } from "../../test/i18n";
import { StalenessIndicator, IssueStalenessNote } from "./staleness-indicator";

const WS_ID = "ws-1";

const apiMocks = vi.hoisted(() => ({ getIssueStaleness: vi.fn() }));
vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => WS_ID }));

function staleRow(over: Partial<StaleIssue> = {}): StaleIssue {
  return {
    issue_id: "issue-1",
    identifier: "MUL-123",
    title: "Wire the staleness endpoint",
    status: "in_review",
    // 3 days back, so the relative age renders as "3d ago".
    since: new Date(Date.now() - 3 * 24 * 60 * 60 * 1000).toISOString(),
    reason: "review_done",
    ...over,
  };
}

function renderWith(
  ui: React.ReactElement,
  opts: { locale?: "en" | "ru" } = {},
) {
  const locale = opts.locale ?? "en";
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale={locale} resources={RESOURCES}>
        {ui}
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  apiMocks.getIssueStaleness.mockReset();
});

describe("StalenessIndicator", () => {
  it("renders a quiet cue naming the reason and the age for a stale issue", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({ stale: [staleRow()] });
    renderWith(<StalenessIndicator issueId="issue-1" />);

    const cue = await screen.findByRole("img", { name: /In review/ });
    expect(cue).toHaveAccessibleName(
      "In review, but its pull requests are already closed · Last activity 3d ago",
    );
    // Quiet by construction: muted, no destructive color, no badge chrome.
    expect(cue.className).toContain("text-muted-foreground");
    expect(cue.className).not.toMatch(/destructive|red|bg-/);
  });

  it("renders nothing for a fresh issue", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({ stale: [staleRow()] });
    const { container } = renderWith(<StalenessIndicator issueId="issue-fresh" />);
    await vi.waitFor(() => expect(apiMocks.getIssueStaleness).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing — and never throws — when the query errors", async () => {
    apiMocks.getIssueStaleness.mockRejectedValue(new Error("404 not found"));
    const { container } = renderWith(<StalenessIndicator issueId="issue-1" />);
    await vi.waitFor(() => expect(apiMocks.getIssueStaleness).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the response carries no stale rows", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({ stale: [] });
    const { container } = renderWith(<StalenessIndicator issueId="issue-1" />);
    await vi.waitFor(() => expect(apiMocks.getIssueStaleness).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("falls back to generic copy for a reason it has never heard of", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({
      stale: [staleRow({ reason: "date_slipped_in_slack" })],
    });
    renderWith(<StalenessIndicator issueId="issue-1" />);

    expect(
      await screen.findByRole("img", {
        name: "This issue may no longer match reality · Last activity 3d ago",
      }),
    ).toBeInTheDocument();
  });

  it("drops the age line rather than printing an invalid date", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({
      stale: [staleRow({ since: "" })],
    });
    renderWith(<StalenessIndicator issueId="issue-1" />);

    expect(
      await screen.findByRole("img", {
        name: "In review, but its pull requests are already closed",
      }),
    ).toBeInTheDocument();
  });

  it("localizes the reason", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({
      stale: [staleRow({ reason: "idle" })],
    });
    renderWith(<StalenessIndicator issueId="issue-1" />, { locale: "ru" });

    expect(
      await screen.findByRole("img", {
        name: "В работе, но ничего не движется · Последняя активность 3 дн назад",
      }),
    ).toBeInTheDocument();
  });
});

describe("IssueStalenessNote", () => {
  it("states the reason and the age in one muted sentence", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({ stale: [staleRow()] });
    renderWith(<IssueStalenessNote issueId="issue-1" />);

    const note = await screen.findByText(
      "This issue looks stale — it's in review, but its pull requests were merged or closed 3d ago.",
    );
    expect(note.closest("p")?.className).toContain("text-muted-foreground");
  });

  it("uses the generic sentence for an unknown reason", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({
      stale: [staleRow({ reason: "date_slipped_in_slack" })],
    });
    renderWith(<IssueStalenessNote issueId="issue-1" />);

    expect(
      await screen.findByText("This issue looks stale — the last activity was 3d ago."),
    ).toBeInTheDocument();
  });

  it("falls back to the timeless wording when `since` is unusable", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({
      stale: [staleRow({ reason: "blocked_quiet", since: "not-a-date" })],
    });
    renderWith(<IssueStalenessNote issueId="issue-1" />);

    expect(
      await screen.findByText("Blocked, with no update since then"),
    ).toBeInTheDocument();
  });

  it("renders nothing for a fresh issue", async () => {
    apiMocks.getIssueStaleness.mockResolvedValue({ stale: [staleRow()] });
    const { container } = renderWith(<IssueStalenessNote issueId="issue-fresh" />);
    await vi.waitFor(() => expect(apiMocks.getIssueStaleness).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
