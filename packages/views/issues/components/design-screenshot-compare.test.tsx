import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { DesignScreenshotCompare } from "./design-screenshot-compare";

// DesignScreenshotCompare is the design lens's primary visual pane: the
// Figma reference vs built screenshot the design-compare QA check attached
// as evidence (docs/design-stage-research.md §4). Sourcing logic itself is
// covered in packages/core/design/screenshots.test.ts — this test only
// covers the pane's own composition: empty state vs pair rendering,
// image src resolution, and figma/built labeling.

const apiMocks = vi.hoisted(() => ({
  listTimeline: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));

function image(over: Record<string, unknown> = {}) {
  return {
    id: "att-1",
    workspace_id: "ws-1",
    issue_id: "issue-1",
    comment_id: "c1",
    chat_session_id: null,
    chat_message_id: null,
    uploader_type: "agent",
    uploader_id: "agent-1",
    filename: "screenshot.png",
    url: "https://cdn.example/screenshot.png",
    download_url: "https://cdn.example/screenshot.png?sig=1",
    markdown_url: "https://cdn.example/screenshot.png",
    content_type: "image/png",
    size_bytes: 1024,
    created_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

function qaResultComment(attachments: ReturnType<typeof image>[]) {
  return {
    type: "comment",
    id: "c1",
    actor_type: "agent",
    actor_id: "agent-1",
    created_at: "2026-01-01T00:00:00Z",
    content: "```qa-result\n{\"verdict\":\"pass\",\"summary\":\"\",\"commands\":[]}\n```",
    attachments,
  };
}

function renderPane() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <DesignScreenshotCompare issueId="issue-1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DesignScreenshotCompare", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the empty state when no design-compare screenshots resolve", async () => {
    apiMocks.listTimeline.mockResolvedValue([]);
    renderPane();

    await screen.findByText("No screenshots yet — they appear here after a design-compare QA run.");
  });

  it("renders a figma/built image pair from the newest qa-result comment", async () => {
    const figma = image({ id: "f1", filename: "figma-208-5147.png" });
    const built = image({ id: "b1", filename: "screenshot.png" });
    apiMocks.listTimeline.mockResolvedValue([qaResultComment([figma, built])]);
    renderPane();

    await screen.findByText("Figma reference");
    expect(screen.getByText("Built")).toBeInTheDocument();

    const images = screen.getAllByRole("img") as HTMLImageElement[];
    expect(images).toHaveLength(2);
    expect(images.map((img) => img.src)).toEqual(
      expect.arrayContaining([figma.download_url, built.download_url]),
    );
    expect(
      screen.queryByText("No screenshots yet — they appear here after a design-compare QA run."),
    ).not.toBeInTheDocument();
  });

  it("renders an unmatched figma screenshot solo (no built counterpart)", async () => {
    const figma = image({ id: "f1", filename: "figma-1-1.png" });
    apiMocks.listTimeline.mockResolvedValue([qaResultComment([figma])]);
    renderPane();

    await screen.findByText("Figma reference");
    expect(screen.queryByText("Built")).not.toBeInTheDocument();
    expect(screen.getAllByRole("img")).toHaveLength(1);
  });
});
