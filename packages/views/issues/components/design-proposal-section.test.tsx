import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enIssues from "../../locales/en/issues.json";
import { DesignProposalSection } from "./design-proposal-section";

const mocks = vi.hoisted(() => ({
  useQuery: vi.fn(),
  invalidateQueries: vi.fn(),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQuery: mocks.useQuery,
    useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
  };
});
vi.mock("@agora/core/api", () => ({ api: { sliceAction: vi.fn(), createDesignReview: vi.fn() } }));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ settings: () => "/acme/settings" }),
}));
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/issues/issue-1",
    searchParams: new URLSearchParams(),
    getShareableUrl: vi.fn(),
  }),
}));
vi.mock("../../platform", () => ({ openExternal: vi.fn() }));
vi.mock("../../editor", () => ({
  useAttachmentPreview: () => ({ open: vi.fn(), tryOpen: vi.fn(), modal: null }),
}));

const BLOCKED_PROPOSAL = `\`\`\`design-proposal
{"status":"blocked","reason":"credential_missing","reason_detail":"Figma credential missing","figma":[{"url":"https://www.figma.com/design/5KEkQk9YUgcq9ooDTlgQVW/Mytrion?node-id=3-2","file_key":"5KEkQk9YUgcq9ooDTlgQVW","node_id":"3:2"}],"screens":[],"components":[],"deviations":[],"sub_issues":[],"open_questions":[]}
\`\`\``;

describe("DesignProposalSection", () => {
  beforeEach(() => {
    mocks.useQuery.mockImplementation((options: { queryKey: readonly unknown[] }) => {
      if (options.queryKey.includes("timeline")) {
        return {
          data: [
            {
              type: "comment",
              id: "comment-1",
              actor_type: "agent",
              actor_id: "agent-1",
              content: BLOCKED_PROPOSAL,
              created_at: "2026-08-13T00:00:00Z",
            },
          ],
        };
      }
      return {
        data: {
          description:
            "https://www.figma.com/design/5KEkQk9YUgcq9ooDTlgQVW/Mytrion?node-id=3-2",
          labels: [{ name: "design:proposed" }],
        },
      };
    });
  });

  it("keeps a blocked proposal reviewable while preventing approval", async () => {
    render(
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <DesignProposalSection issueId="issue-1" />
      </I18nProvider>,
    );

    const openReview = screen.getByRole("button", { name: "Open review" });
    fireEvent.click(openReview);

    expect(await screen.findByTitle("Figma design preview")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
  });
});
