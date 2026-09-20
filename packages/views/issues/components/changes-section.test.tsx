import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { IssueChange, IssueChangePatchResponse } from "@agora/core/types";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

// The section is a read surface over two endpoints, so the endpoints are the
// seam: mock the query options and assert what the user ends up looking at.
vi.mock("@agora/core/github/queries", async () => {
  const actual = await vi.importActual<typeof import("@agora/core/github/queries")>(
    "@agora/core/github/queries",
  );
  return {
    ...actual,
    issueChangesOptions: (issueId: string) => ({
      queryKey: ["github", "issue-changes", issueId],
      queryFn: async () => ({ changes: mockChanges }),
      enabled: !!issueId,
    }),
    issueChangePatchOptions: (
      issueId: string,
      prNumber: number,
      path: string,
      enabled: boolean,
    ) => ({
      queryKey: ["github", "issue-change-patch", issueId, prNumber, path],
      queryFn: async () => {
        patchCalls.push(path);
        return mockPatch;
      },
      enabled: enabled && !!issueId && !!path,
    }),
  };
});

import { ChangesSection } from "./changes-section";

let mockChanges: IssueChange[] = [];
let mockPatch: IssueChangePatchResponse = { patch: null, reason: "" };
let patchCalls: string[] = [];

function makeChange(overrides: Partial<IssueChange> = {}): IssueChange {
  return {
    pr_number: 42,
    title: "Fix the login redirect",
    state: "open",
    html_url: "https://github.com/acme/widget/pull/42",
    repo_owner: "acme",
    repo_name: "widget",
    additions: 12,
    deletions: 3,
    changed_files: 2,
    files_source: "github",
    files: [
      { path: "server/internal/handler/auth.go", status: "modified", additions: 10, deletions: 2 },
      { path: "docs/auth.md", status: "added", additions: 2, deletions: 0 },
    ],
    ...overrides,
  };
}

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <ChangesSection issueId="issue-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockChanges = [];
  mockPatch = { patch: null, reason: "" };
  patchCalls = [];
});

describe("ChangesSection", () => {
  it("renders nothing at all when the issue has no pull request", async () => {
    mockChanges = [];
    const { container } = renderSection();
    // Give the query a tick to settle, then assert the section never appeared:
    // an issue that produced no code must not carry an empty "no changes" card.
    await waitFor(() => expect(container.querySelector("[data-testid='changes-section']")).toBeNull());
    expect(screen.queryByTestId("changes-pr")).not.toBeInTheDocument();
  });

  it("lists the PR header and every changed file with its counts", async () => {
    mockChanges = [makeChange()];
    renderSection();

    expect(await screen.findByTestId("changes-pr")).toBeInTheDocument();
    expect(screen.getByText("acme/widget#42")).toBeInTheDocument();
    expect(screen.getByText("Open")).toBeInTheDocument();
    expect(screen.getByText("+12")).toBeInTheDocument();
    expect(screen.getByText("−3")).toBeInTheDocument();
    expect(screen.getByText("server/internal/handler/auth.go")).toBeInTheDocument();
    expect(screen.getByText("docs/auth.md")).toBeInTheDocument();
    expect(screen.getByText("+10")).toBeInTheDocument();
    // The GitHub link survives as the secondary affordance next to the header.
    expect(screen.getByRole("link", { name: /View on GitHub/ })).toHaveAttribute(
      "href",
      "https://github.com/acme/widget/pull/42",
    );
  });

  it("expands a file into its unified diff, fetched only on click", async () => {
    mockChanges = [makeChange()];
    mockPatch = { patch: "@@ -1,2 +1,2 @@\n-old line\n+new line", reason: "" };
    renderSection();

    const rows = await screen.findAllByTestId("changed-file");
    expect(patchCalls).toEqual([]); // nothing fetched until the user asks

    await userEvent.click(rows[0]!);

    expect(await screen.findByTestId("changed-file-diff")).toBeInTheDocument();
    expect(screen.getByText("new line")).toBeInTheDocument();
    expect(screen.getByText("old line")).toBeInTheDocument();
    expect(patchCalls).toEqual(["server/internal/handler/auth.go"]);
  });

  it("says why there is no diff instead of rendering an empty pane", async () => {
    mockChanges = [makeChange()];
    mockPatch = { patch: null, reason: "patch_unavailable" };
    renderSection();

    const rows = await screen.findAllByTestId("changed-file");
    await userEvent.click(rows[0]!);

    const fallback = await screen.findByTestId("changed-file-no-diff");
    expect(fallback).toHaveTextContent(/binary or its diff is too large/i);
    expect(screen.queryByTestId("changed-file-diff")).not.toBeInTheDocument();
  });

  it("falls back to a generic explanation for a reason it does not know", async () => {
    mockChanges = [makeChange()];
    mockPatch = { patch: null, reason: "some_future_server_reason" };
    renderSection();

    const rows = await screen.findAllByTestId("changed-file");
    await userEvent.click(rows[0]!);

    const fallback = await screen.findByTestId("changed-file-no-diff");
    expect(fallback).toHaveTextContent(/couldn't be loaded/i);
  });

  it("hides per-file counts when the list came from stored paths", async () => {
    // files_source=stored means "paths only" — rendering +0 −0 would be a
    // confident lie about a file that definitely changed.
    mockChanges = [
      makeChange({
        files_source: "stored",
        files: [{ path: "server/a.go", status: "", additions: 0, deletions: 0 }],
      }),
    ];
    renderSection();

    expect(await screen.findByText("server/a.go")).toBeInTheDocument();
    expect(screen.queryByText("+0")).not.toBeInTheDocument();
    expect(screen.queryByText("−0")).not.toBeInTheDocument();
  });

  it("says the file list is unavailable rather than showing an empty PR card", async () => {
    mockChanges = [makeChange({ files_source: "none", files: [] })];
    renderSection();

    expect(await screen.findByTestId("changes-pr")).toBeInTheDocument();
    expect(screen.getByText(/file list isn't available/i)).toBeInTheDocument();
  });

  it("names the file a rename came from", async () => {
    mockChanges = [
      makeChange({
        files: [
          {
            path: "server/new.go",
            previous_path: "server/old.go",
            status: "renamed",
            additions: 1,
            deletions: 1,
          },
        ],
      }),
    ];
    renderSection();
    expect(await screen.findByText("Renamed from server/old.go")).toBeInTheDocument();
  });
});
