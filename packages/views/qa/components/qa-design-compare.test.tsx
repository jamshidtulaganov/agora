import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { QADesignCompare } from "./qa-design-compare";

// QADesignCompare renders the advisory design-verification result: verdict
// badge, mismatch table, design-system lint, and — when `issueId` is passed
// — the design-compare screenshots (figma reference vs built), reused by
// both the QA lens (compact) and the design lens's right column (full). See
// packages/core/design/screenshots.test.ts for the pairing logic itself.

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

function renderCompare(ui: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
    </I18nProvider>,
  );
}

describe("QADesignCompare", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.listTimeline.mockResolvedValue([]);
  });

  it("renders nothing when there is no design result", () => {
    const { container } = renderCompare(<QADesignCompare design={null} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the verdict + mismatch table without issueId (backward compatible, no images)", async () => {
    renderCompare(
      <QADesignCompare
        design={{
          verdict: "fail",
          reference_node: "208:5147",
          mismatches: [{ kind: "color", selector: ".btn", expected: "#2563EB", actual: "#000000" }],
        }}
      />,
    );

    await screen.findByText("mismatch");
    expect(screen.getByText("color")).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });

  it("renders the figma/built screenshot pair when issueId resolves qa-result attachments", async () => {
    const figma = image({ id: "f1", filename: "figma-208-5147.png" });
    const built = image({ id: "b1", filename: "screenshot.png" });
    apiMocks.listTimeline.mockResolvedValue([
      {
        type: "comment",
        id: "c1",
        actor_type: "agent",
        actor_id: "agent-1",
        created_at: "2026-01-01T00:00:00Z",
        content: "```qa-result\n{\"verdict\":\"pass\",\"summary\":\"\",\"commands\":[]}\n```",
        attachments: [figma, built],
      },
    ]);

    renderCompare(
      <QADesignCompare
        design={{ verdict: "pass", reference_node: "208:5147", mismatches: [] }}
        issueId="issue-1"
      />,
    );

    await screen.findByText("Figma reference");
    expect(screen.getByText("Built")).toBeInTheDocument();
    expect(screen.getAllByRole("img")).toHaveLength(2);
  });
});
