import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { setApiInstance } from "@agora/core/api";
import type { ApiClient } from "@agora/core/api";
import { I18nProvider } from "@agora/core/i18n/react";
import type { KnowledgeDoc } from "@agora/core/knowledge";
import { WorkspaceSlugProvider } from "@agora/core/paths";
import { workspaceKeys } from "@agora/core/workspace/queries";
import type { Workspace } from "@agora/core/types";
import { NavigationProvider, type NavigationAdapter } from "../../navigation";
import { RESOURCES } from "../../locales";
import { KnowledgePage } from "./knowledge-page";

const toast = vi.hoisted(() =>
  Object.assign(vi.fn(), {
    success: vi.fn(),
    error: vi.fn(),
    loading: vi.fn(() => "toast-1"),
  }),
);
vi.mock("sonner", () => ({ toast }));

const authState = vi.hoisted(() => ({ user: { id: "user-1" } }));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector?: (state: typeof authState) => unknown) => (selector ? selector(authState) : authState),
    { getState: () => authState },
  ),
}));

const WORKSPACE = {
  id: "ws-1",
  name: "Collections",
  slug: "acme",
  description: "",
  context: "We are the Collections team.",
  settings: {},
  repos: [],
  issue_prefix: "COL",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
} satisfies Workspace;

function doc(overrides: Partial<KnowledgeDoc> = {}): KnowledgeDoc {
  return {
    id: "doc-1",
    title: "Collections SOP",
    source: "upload",
    filename: "collections-sop.pdf",
    content_type: "application/pdf",
    size_bytes: 2 * 1024 * 1024,
    status: "ready",
    pinned: false,
    page_count: 12,
    chunk_count: 30,
    attachment_id: "att-1",
    created_by_name: "Dilnoza",
    created_at: "2026-09-25T10:00:00Z",
    updated_at: "2026-09-25T10:00:00Z",
    ...overrides,
  };
}

const api = {
  listWorkspaces: vi.fn(),
  listMembers: vi.fn(),
  listKnowledge: vi.fn(),
  getKnowledgeDoc: vi.fn(),
  searchKnowledge: vi.fn(),
  createKnowledgeDoc: vi.fn(),
  updateKnowledgeDoc: vi.fn(),
  deleteKnowledgeDoc: vi.fn(),
  reprocessKnowledgeDoc: vi.fn(),
  uploadFile: vi.fn(),
  updateWorkspace: vi.fn(),
  getAttachment: vi.fn(),
};

function renderPage(search = "") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(workspaceKeys.list(), [WORKSPACE]);
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/knowledge",
    searchParams: new URLSearchParams(search),
    getShareableUrl: (p) => p,
  };
  const utils = render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <NavigationProvider value={nav}>
          <WorkspaceSlugProvider slug="acme">
            <KnowledgePage />
          </WorkspaceSlugProvider>
        </NavigationProvider>
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...utils, nav, qc };
}

beforeEach(() => {
  for (const fn of Object.values(api)) fn.mockReset();
  toast.mockClear();
  toast.success.mockClear();
  toast.error.mockClear();
  toast.loading.mockClear();
  api.listWorkspaces.mockResolvedValue([WORKSPACE]);
  api.listMembers.mockResolvedValue([]);
  api.searchKnowledge.mockResolvedValue({ results: [] });
  setApiInstance(api as unknown as ApiClient);
});

afterEach(() => {
  cleanup();
});

describe("KnowledgePage — owners and admins", () => {
  beforeEach(() => {
    api.listKnowledge.mockResolvedValue({
      can_manage: true,
      documents: [
        doc(),
        doc({ id: "doc-2", title: "Rate sheet", filename: "rates.xlsx", status: "processing", page_count: undefined, chunk_count: 0 }),
        doc({ id: "doc-3", title: "Old policy", status: "failed", error: "The file is password-protected." }),
        doc({ id: "doc-4", title: "Scanned contract", status: "needs_ocr", error: "No text layer." }),
      ],
    });
  });

  it("shows the page, the instructions and every document with its status", async () => {
    renderPage();

    expect(screen.getByRole("heading", { name: "Knowledge" })).toBeInTheDocument();
    expect(
      screen.getByText("What this team runs on. The Assistant and agents answer and work from it."),
    ).toBeInTheDocument();
    expect(screen.getByText("Instructions for AI")).toBeInTheDocument();
    expect(screen.getByText("Always given to the Assistant and agents in this workspace.")).toBeInTheDocument();
    expect(screen.getByText("We are the Collections team.")).toBeInTheDocument();

    expect(await screen.findByText("Collections SOP")).toBeInTheDocument();
    expect(screen.getByText(/2\.0 MB · 12 pages · 30 sections · Added by Dilnoza/)).toBeInTheDocument();
    expect(screen.getByText("Ready")).toBeInTheDocument();
    expect(screen.getByText("Reading…")).toBeInTheDocument();
    expect(screen.getByText("Couldn't read")).toBeInTheDocument();
    expect(screen.getByText("The file is password-protected.")).toBeInTheDocument();
    expect(screen.getByText("Scanned PDF — needs text")).toBeInTheDocument();

    expect(screen.getByRole("button", { name: "Add files" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New note" })).toBeInTheDocument();
  });

  it("pins a document with the star before the server answers", async () => {
    let resolve!: (value: KnowledgeDoc) => void;
    api.updateKnowledgeDoc.mockReturnValue(new Promise<KnowledgeDoc>((r) => (resolve = r)));
    renderPage();

    const rows = await screen.findAllByTestId("knowledge-doc-row");
    const star = within(rows[0]!).getByRole("button", { name: "Always include" });
    expect(star).toHaveAttribute("aria-pressed", "false");

    await userEvent.click(star);
    expect(api.updateKnowledgeDoc).toHaveBeenCalledWith("doc-1", { pinned: true });
    await waitFor(() => expect(star).toHaveAttribute("aria-pressed", "true"));
    resolve(doc({ pinned: true }));
  });

  it("removes a document only after the confirm", async () => {
    // Held open so the check below sees the optimistic removal, not a refetch.
    api.deleteKnowledgeDoc.mockReturnValue(new Promise<void>(() => {}));
    renderPage();

    const rows = await screen.findAllByTestId("knowledge-doc-row");
    await userEvent.click(within(rows[0]!).getByRole("button", { name: "Document actions" }));
    await userEvent.click(await screen.findByRole("menuitem", { name: "Remove" }));

    expect(await screen.findByText('Remove "Collections SOP"?')).toBeInTheDocument();
    expect(api.deleteKnowledgeDoc).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Remove" }));

    expect(api.deleteKnowledgeDoc).toHaveBeenCalledWith("doc-1");
    await waitFor(() => expect(screen.queryByText("Collections SOP")).not.toBeInTheDocument());
  });

  it("uploads each allowed file, then adds it; a wrong type is refused before uploading", async () => {
    api.uploadFile.mockResolvedValue({ id: "att-9", url: "", filename: "refunds.pdf" });
    api.createKnowledgeDoc.mockResolvedValue(doc({ id: "doc-9", title: "refunds", status: "processing" }));
    renderPage();
    await screen.findByText("Collections SOP");

    const input = screen.getByTestId("knowledge-file-input") as HTMLInputElement;
    const pdf = new File(["%PDF-1.4"], "refunds.pdf", { type: "application/pdf" });
    const png = new File(["img"], "photo.png", { type: "image/png" });
    await userEvent.upload(input, [png, pdf], { applyAccept: false });

    await waitFor(() => expect(api.createKnowledgeDoc).toHaveBeenCalledWith({ attachment_id: "att-9" }));
    expect(api.uploadFile).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledWith("Couldn't add photo.png", {
      description: "Only PDF, Word (.docx), Excel (.xlsx), CSV, Markdown and text files can be added.",
    });
    await waitFor(() =>
      expect(toast.success).toHaveBeenCalledWith("Added refunds.pdf", expect.objectContaining({ id: "toast-1" })),
    );
  });

  it("shows the server's reason when a file is refused", async () => {
    api.uploadFile.mockResolvedValue({ id: "att-9", url: "", filename: "big.pdf" });
    api.createKnowledgeDoc.mockRejectedValue(new Error("the knowledge base is full (300 documents)"));
    renderPage();
    await screen.findByText("Collections SOP");

    await userEvent.upload(
      screen.getByTestId("knowledge-file-input") as HTMLInputElement,
      new File(["%PDF"], "big.pdf", { type: "application/pdf" }),
    );

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith("Couldn't add big.pdf", {
        id: "toast-1",
        description: "the knowledge base is full (300 documents)",
      }),
    );
  });

  it("edits Instructions for AI in place", async () => {
    let saved: Workspace = WORKSPACE;
    api.listWorkspaces.mockImplementation(async () => [saved]);
    api.updateWorkspace.mockImplementation(async (_id: string, patch: { context: string }) => {
      saved = { ...WORKSPACE, ...patch };
      return saved;
    });
    renderPage();

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));
    const box = screen.getByRole("textbox", { name: "Instructions for AI" });
    await userEvent.clear(box);
    await userEvent.type(box, "Be polite.");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(api.updateWorkspace).toHaveBeenCalledWith("ws-1", { context: "Be polite." });
    expect(await screen.findByText("Be polite.")).toBeInTheDocument();
  });

  it("searches and opens the hit at its section", async () => {
    api.searchKnowledge.mockResolvedValue({
      results: [
        {
          chunk_id: "c4",
          doc_id: "doc-1",
          doc_title: "Collections SOP",
          section: 3,
          heading_path: "Collections SOP › Write-offs",
          location: "p. 4",
          snippet: "Write-offs over $500 need a manager's approval.",
          cite: "kb:abcd1234",
        },
      ],
    });
    const { nav } = renderPage();
    await screen.findByText("Collections SOP");

    await userEvent.type(screen.getByRole("searchbox", { name: "Search documents" }), "write-off");
    const hit = await screen.findByText("Write-offs over $500 need a manager's approval.");
    expect(api.searchKnowledge).toHaveBeenCalledWith("write-off", 10);

    await userEvent.click(hit);
    expect(nav.replace).toHaveBeenCalledWith("/acme/knowledge?doc=doc-1&section=3");
  });

  it("shows an empty state with the upload button when nothing is added yet", async () => {
    api.listKnowledge.mockResolvedValue({ can_manage: true, documents: [] });
    renderPage();

    expect(
      await screen.findByText(
        "Add your team's SOPs, policies and price lists. The Assistant and agents will answer and work from them.",
      ),
    ).toBeInTheDocument();
    // One in the header, one in the empty state.
    expect(screen.getAllByRole("button", { name: "Add files" })).toHaveLength(2);
    expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();
  });
});

describe("KnowledgePage — members", () => {
  it("reads without any way to change things", async () => {
    api.listKnowledge.mockResolvedValue({ can_manage: false, documents: [doc({ pinned: true })] });
    renderPage();

    expect(await screen.findByText("Collections SOP")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add files" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "New note" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Document actions" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Always include" })).not.toBeInTheDocument();
    // A pinned document still says so.
    expect(screen.getByLabelText("Always include")).toBeInTheDocument();
    expect(screen.queryByTestId("knowledge-file-input")).not.toBeInTheDocument();
  });

  it("asks them to find an owner or admin when the base is empty", async () => {
    api.listKnowledge.mockResolvedValue({ can_manage: false, documents: [] });
    renderPage();

    expect(await screen.findByText("Ask a workspace owner or admin to add documents.")).toBeInTheDocument();
  });

  it("gives an admin the controls even when can_manage came back false", async () => {
    // can_manage is one signal; the caller's role is the second, so a drifted
    // response can't take the controls away from an admin.
    api.listKnowledge.mockResolvedValue({ can_manage: false, documents: [] });
    api.listMembers.mockResolvedValue([{ user_id: "user-1", role: "admin" }]);
    renderPage();

    expect(await screen.findAllByRole("button", { name: "Add files" })).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
  });
});

describe("KnowledgePage — viewer", () => {
  beforeEach(() => {
    api.listKnowledge.mockResolvedValue({ can_manage: false, documents: [doc()] });
    api.getKnowledgeDoc.mockResolvedValue({
      document: doc(),
      chunks: [
        { id: "c0", ord: 0, heading_path: "Collections SOP", location: "p. 1", body: "Welcome." },
        {
          id: "c1",
          ord: 1,
          heading_path: "Collections SOP › Rates",
          location: 'Sheet "Rates", rows 2–41',
          body: "| Tier | Fee |\n| --- | --- |\n| Gold | 2% |",
        },
      ],
    });
  });

  it("deep-links to a section and highlights it", async () => {
    renderPage("?doc=doc-1&section=1");

    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText('Sheet "Rates", rows 2–41')).toBeInTheDocument();
    expect(within(dialog).getByText("Collections SOP › Rates")).toBeInTheDocument();
    // The body renders as markdown — the table is a real table.
    expect(within(dialog).getByRole("table")).toBeInTheDocument();
    const active = dialog.querySelector('[data-ord="1"]');
    expect(active).toHaveAttribute("aria-current", "location");
    expect(dialog.querySelector('[data-ord="0"]')).not.toHaveAttribute("aria-current");
    expect(within(dialog).getByRole("button", { name: "Download original" })).toBeInTheDocument();
    expect(api.getKnowledgeDoc).toHaveBeenCalledWith("doc-1");
  });

  it("offers no download for a note", async () => {
    api.getKnowledgeDoc.mockResolvedValue({
      document: doc({ source: "note", filename: undefined, attachment_id: undefined }),
      chunks: [{ id: "c0", ord: 0, heading_path: "", location: "", body: "Refunds need approval." }],
    });
    renderPage("?doc=doc-1");

    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Refunds need approval.")).toBeInTheDocument();
    expect(within(dialog).queryByRole("button", { name: "Download original" })).not.toBeInTheDocument();
  });

  it("says so when the document can't be opened", async () => {
    api.getKnowledgeDoc.mockResolvedValue({ document: { id: "" }, chunks: [] });
    renderPage("?doc=doc-1");

    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText("Couldn't open this document.")).toBeInTheDocument();
  });
});
