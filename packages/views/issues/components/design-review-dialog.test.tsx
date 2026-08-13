import { describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { DesignProposal } from "@agora/core/design";
import enIssues from "../../locales/en/issues.json";
import { DesignReviewDialog, type DesignProposalVersion } from "./design-review-dialog";

vi.mock("@agora/core/api", () => ({
  api: { createDesignReview: vi.fn() },
}));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/workspace/avatar-url", () => ({
  resolvePublicFileUrl: (url: string) =>
    url.startsWith("/") ? `https://agora.example${url}` : url,
}));

const EMPTY_PROPOSAL: DesignProposal = {
  status: "ok",
  reason: null,
  reason_detail: "",
  figma: [],
  screens: [],
  components: [],
  deviations: [],
  sub_issues: [],
  open_questions: [],
};

function version(proposal: DesignProposal | null = EMPTY_PROPOSAL): DesignProposalVersion {
  return {
    parsed: {
      commentId: "comment-1",
      createdAt: "2026-08-13T00:00:00Z",
      authorId: "agent-1",
      state: proposal ? "ok" : "invalid",
      proposal,
    },
    attachments: [],
  };
}

const ATTACHMENT = {
  id: "attachment-1",
  workspace_id: "ws-1",
  issue_id: "issue-1",
  comment_id: "comment-1",
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "agent",
  uploader_id: "agent-1",
  filename: "composer.png",
  url: "/uploads/composer.png",
  download_url: "/api/attachments/attachment-1/download",
  markdown_url: "https://agora.example/api/attachments/attachment-1/download",
  content_type: "image/png",
  size_bytes: 100,
  created_at: "2026-08-13T00:00:00Z",
};

function renderDialog(versions: DesignProposalVersion[]) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={queryClient}>
        <DesignReviewDialog issueId="issue-1" versions={versions} onClose={vi.fn()} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("DesignReviewDialog", () => {
  it("renders an empty revision as a compact, non-approvable review state", async () => {
    renderDialog([version()]);

    const dialog = await screen.findByRole("dialog");
    expect(dialog).not.toHaveClass("h-[85vh]");
    expect(dialog).toHaveClass("sm:max-w-[1180px]");
    expect(screen.getByText("No review details in this revision")).toBeInTheDocument();
    expect(screen.getByText("Revision v1")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Review note" })).toHaveAttribute(
      "name",
      "design-review-note",
    );
    expect(screen.getByRole("button", { name: "Approve" })).toBeDisabled();
  });

  it("summarizes review coverage and enables approval when the proposal has content", async () => {
    const proposal: DesignProposal = {
      ...EMPTY_PROPOSAL,
      screens: [{ name: "Composer", figma_node_id: "1:2", summary: "Live preview", render: "" }],
      components: [{ name: "Button", verdict: "reuse", code_ref: null, figma_node_id: null, notes: "" }],
      sub_issues: [{ title: "Build composer", description: "Implement the selected design", screens: [], node_ids: [], depends_on: [] }],
    };
    renderDialog([version(proposal)]);

    const coverage = await screen.findByLabelText("Review coverage");
    expect(within(coverage).getByText("Screens")).toBeInTheDocument();
    expect(within(coverage).getByText("Components")).toBeInTheDocument();
    expect(within(coverage).getByText("Sub-issues")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve" })).toBeEnabled();
  });

  it("absolutizes a relative attachment URL for the desktop renderer", async () => {
    const proposal: DesignProposal = {
      ...EMPTY_PROPOSAL,
      screens: [
        { name: "Composer", figma_node_id: "", summary: "Live preview", render: "composer.png" },
      ],
    };
    const proposalVersion = version(proposal);
    proposalVersion.attachments = [ATTACHMENT];
    renderDialog([proposalVersion]);

    expect(await screen.findByRole("img", { name: "Composer" })).toHaveAttribute(
      "src",
      "https://agora.example/api/attachments/attachment-1/download",
    );
  });

  it("embeds the linked Figma node while keeping the source file available", async () => {
    const proposal: DesignProposal = {
      ...EMPTY_PROPOSAL,
      figma: [
        {
          url: "https://www.figma.com/design/5KEkQk9YUgcq9ooDTlgQVW/Mytrion?node-id=3-2",
          file_key: "5KEkQk9YUgcq9ooDTlgQVW",
          node_id: "3:2",
        },
      ],
      screens: [
        { name: "Composer", figma_node_id: "1:7", summary: "Live preview", render: "" },
      ],
    };
    renderDialog([version(proposal)]);

    expect(await screen.findByTitle("Figma design preview")).toHaveAttribute(
      "src",
      "https://embed.figma.com/design/5KEkQk9YUgcq9ooDTlgQVW/Mytrion?node-id=3-2&embed-host=agora&theme=system",
    );
    expect(screen.getByRole("link", { name: "Open in Figma" })).toHaveAttribute(
      "href",
      proposal.figma[0]!.url,
    );
  });
});
