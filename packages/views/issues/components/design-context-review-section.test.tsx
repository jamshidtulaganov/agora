import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { DesignContextReviewSection } from "./design-context-review-section";

const state = vi.hoisted(() => ({ role: "owner", get: vi.fn(), review: vi.fn() }));

vi.mock("@agora/core", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/permissions", () => ({
  useCurrentMember: () => ({ role: state.role, userId: "u1", member: null, isLoading: false }),
}));
vi.mock("@agora/core/api", () => ({
  api: {
    getProjectDesignContext: state.get,
    reviewProjectDesignContext: state.review,
  },
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const emptyContext = {
  version: 1,
  kind: "tokens",
  figma: { library_file_key: "", notes: "" },
  tokens: { colors: {}, typography: {}, spacing: {} },
  components: [],
  conventions: [],
  anti_patterns: [],
  legacy_notes: "",
  screens_reference: "",
  sources: [],
};

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={client}>
        <DesignContextReviewSection projectId="p1" />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DesignContextReviewSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    state.role = "owner";
    state.review.mockResolvedValue({});
    state.get.mockResolvedValue({
      active: { context: { ...emptyContext, tokens: { ...emptyContext.tokens, colors: { brand: "blue" } } } },
      proposal: {
        revision: 3,
        base_revision: 2,
        freshness: { status: "fresh", stale_sources: [] },
        context: {
          ...emptyContext,
          tokens: { ...emptyContext.tokens, colors: { brand: "navy" } },
          components: [{ name: "Button", code_ref: "ui/button", figma_node_id: null, usage: "Actions" }],
          conventions: ["Use tokens"],
          sources: [{ kind: "repository", locator: "packages/ui", revision: "abc123", content_hash: "abcdef12", captured_at: "2026-08-11T10:00:00Z" }],
        },
      },
      history: [],
    });
  });

  it("shows a reviewable provenance diff and approves with the proposal base revision", async () => {
    renderSection();

    await screen.findByText("Revision 3 based on revision 2");
    expect(screen.getByText("Token changes (1)")).toBeInTheDocument();
    expect(screen.getByText("Component changes (1)")).toBeInTheDocument();
    expect(screen.getByText("Source provenance (1)")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => expect(state.review).toHaveBeenCalledWith("p1", "approve", 2));
  });

  it("keeps review controls hidden from regular members", async () => {
    state.role = "member";
    renderSection();

    await screen.findByText("An owner or admin must review this proposal.");
    expect(screen.queryByRole("button", { name: "Approve" })).not.toBeInTheDocument();
  });
});
