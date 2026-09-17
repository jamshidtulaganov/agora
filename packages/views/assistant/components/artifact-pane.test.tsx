import type { ComponentProps } from "react";
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { AssistantArtifact } from "@agora/core/types";
import { RESOURCES } from "../../locales";

const mockGetArtifact = vi.hoisted(() => vi.fn());
const mockListRevisions = vi.hoisted(() => vi.fn());
const mockGetRevision = vi.hoisted(() => vi.fn());
const mockListSessionArtifacts = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/assistant", () => ({
  assistantArtifactOptions: (id: string) => ({
    queryKey: ["assistant", "artifact", id],
    queryFn: () => mockGetArtifact(id),
    enabled: !!id,
    retry: false,
  }),
  assistantArtifactRevisionsOptions: (id: string) => ({
    queryKey: ["assistant", "artifacts", id, "revisions"],
    queryFn: () => mockListRevisions(id),
    enabled: !!id,
    retry: false,
  }),
  assistantArtifactRevisionOptions: (id: string, version: number | null) => ({
    queryKey: ["assistant", "artifacts", id, "revisions", version ?? 0],
    queryFn: () => mockGetRevision(id, version),
    enabled: !!id && !!version && version > 0,
    retry: false,
  }),
  assistantSessionArtifactListOptions: (sessionId: string) => ({
    queryKey: ["assistant", "session-artifacts", sessionId],
    queryFn: () => mockListSessionArtifacts(sessionId),
    enabled: !!sessionId,
    retry: false,
  }),
}));

import { ArtifactPane } from "./artifact-pane";

function artifact(overrides: Partial<AssistantArtifact>): AssistantArtifact {
  return {
    id: "art-1",
    session_id: "session-1",
    title: "Agent usage by day",
    kind: "chart",
    content: "",
    version: 1,
    created_at: "2026-09-16T10:00:00Z",
    updated_at: "2026-09-16T10:00:00Z",
    ...overrides,
  };
}

function renderPane(props: Partial<ComponentProps<typeof ArtifactPane>> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <ArtifactPane artifactId="art-1" onClose={props.onClose ?? vi.fn()} {...props} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

const CHART_SPEC = JSON.stringify({
  type: "bar",
  x: "day",
  series: [{ key: "runs", label: "Runs" }],
  rows: [
    { day: "Mon", runs: 4 },
    { day: "Tue", runs: 7 },
  ],
});

beforeEach(() => {
  vi.clearAllMocks();
  // Default posture for every existing test: the revisions endpoints are not
  // deployed (404) and the session list is empty — so the header must look
  // exactly as it did before the workbench landed.
  mockListRevisions.mockRejectedValue(new Error("404"));
  mockGetRevision.mockRejectedValue(new Error("404"));
  mockListSessionArtifacts.mockResolvedValue([]);
});

afterEach(() => {
  cleanup();
});

describe("ArtifactPane — kind rendering", () => {
  it("switches between safe HTML preview and literal source", async () => {
    mockGetArtifact.mockResolvedValue(artifact({ kind: "html", content: "<script>window.secret = true</script><p>Preview</p>" }));
    const { container } = renderPane();
    await screen.findByRole("button", { name: "Code" });
    expect(container.querySelector("iframe")?.getAttribute("sandbox")).toBe("allow-scripts");
    fireEvent.click(screen.getByRole("button", { name: "Code" }));
    expect(container.querySelector("iframe")).not.toBeInTheDocument();
    expect(container.querySelector("code")?.textContent).toContain("<script>");
    expect(container.querySelector("script")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(container.querySelector("iframe")).toBeInTheDocument();
  });
  it("renders a chart artifact through the app's chart stack", async () => {
    mockGetArtifact.mockResolvedValue(artifact({ kind: "chart", content: CHART_SPEC, version: 2 }));
    const { container } = renderPane();

    await screen.findByText("Agent usage by day");
    // Header carries the kind label + version.
    expect(screen.getByText("Chart · v2")).toBeInTheDocument();
    // The native Recharts container — NOT the raw-content downgrade.
    await waitFor(() => {
      expect(container.querySelector('[data-slot="chart"]')).toBeInTheDocument();
    });
    expect(screen.queryByText(/showing its raw content/)).not.toBeInTheDocument();
  });

  it("renders a table artifact as a real table", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({
        title: "Open issues",
        kind: "table",
        content: JSON.stringify({
          columns: ["Issue", "Age"],
          rows: [["MUL-1", 3], ["MUL-2", null]],
        }),
      }),
    );
    renderPane();

    await screen.findByText("Open issues");
    expect(screen.getByRole("columnheader", { name: "Issue" })).toBeInTheDocument();
    expect(screen.getByRole("cell", { name: "MUL-1" })).toBeInTheDocument();
    // A null cell renders as an em dash rather than "null".
    expect(screen.getByRole("cell", { name: "—" })).toBeInTheDocument();
  });

  it("renders html artifacts ONLY inside the allow-scripts sandbox iframe", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({
        title: "Burndown widget",
        kind: "html",
        content: "<!doctype html><p>hi</p>",
      }),
    );
    const { container } = renderPane();

    await screen.findByText("Burndown widget");
    const iframe = container.querySelector("iframe");
    expect(iframe).toBeInTheDocument();
    // Security invariant: never allow-same-origin (plan §7.1).
    expect(iframe?.getAttribute("sandbox")).toBe("allow-scripts");
    expect(iframe?.getAttribute("srcdoc")).toContain("<p>hi</p>");
  });
});

describe("ArtifactPane — drift downgrades", () => {
  it("downgrades a malformed chart spec to raw content instead of crashing", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({ kind: "chart", content: '{"type":"radar","x":"day"}' }),
    );
    const { container } = renderPane();

    await screen.findByText(/showing its raw content/);
    expect(screen.getByText('{"type":"radar","x":"day"}')).toBeInTheDocument();
    expect(container.querySelector('[data-slot="chart"]')).not.toBeInTheDocument();
  });

  it("downgrades an unknown future kind to raw content with a generic label", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({ title: "Flow", kind: "mermaid", content: "graph TD; A-->B;" }),
    );
    renderPane();

    await screen.findByText(/showing its raw content/);
    expect(screen.getByText("graph TD; A-->B;")).toBeInTheDocument();
    // The raw enum value never leaks into the UI.
    expect(screen.getByText("Artifact · v1")).toBeInTheDocument();
  });
});

describe("ArtifactPane — backend not there yet", () => {
  it("shows a quiet unavailable state when the endpoint 404s", async () => {
    mockGetArtifact.mockRejectedValue(new Error("404"));
    renderPane();

    expect(await screen.findByText("This artifact isn't available.")).toBeInTheDocument();
  });

  it("treats a drifted (EMPTY fallback) response as unavailable", async () => {
    // What parseWithFallback returns when the body fails schema validation.
    mockGetArtifact.mockResolvedValue(
      artifact({ id: "", title: "", kind: "", content: "", version: 1 }),
    );
    renderPane();

    expect(await screen.findByText("This artifact isn't available.")).toBeInTheDocument();
  });
});

describe("ArtifactPane — version picker", () => {
  const revisions = [
    { id: "rev-3", artifact_id: "art-1", version: 3, title: "Sprint report", created_at: "2026-09-16T12:00:00Z" },
    { id: "rev-2", artifact_id: "art-1", version: 2, title: "Sprint report", created_at: "2026-09-16T11:00:00Z" },
    { id: "rev-1", artifact_id: "art-1", version: 1, title: "Sprint report", created_at: "2026-09-16T10:00:00Z" },
  ];

  it("renders a past revision read-only and returns to the latest", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({ title: "Sprint report", kind: "markdown", content: "Latest body", version: 3 }),
    );
    mockListRevisions.mockResolvedValue(revisions);
    mockGetRevision.mockResolvedValue({ ...revisions[2], content: "First body" });
    const onEdit = vi.fn();

    renderPane({ onEdit });

    // The badge becomes a control only once there is history to pick from.
    const picker = await screen.findByRole("button", { name: "Version history" });
    expect(picker).toHaveTextContent("v3");
    fireEvent.click(picker);
    fireEvent.click(screen.getByRole("menuitem", { name: /^v1/ }));

    expect(await screen.findByText("First body")).toBeInTheDocument();
    expect(mockGetRevision).toHaveBeenCalledWith("art-1", 1);
    expect(screen.getByText("Viewing v1 of v3 — read-only")).toBeInTheDocument();
    expect(screen.queryByText("Latest body")).not.toBeInTheDocument();
    // Read-only means read-only: editing acts on the live artifact, so the
    // action is withdrawn while a past version is on screen.
    expect(screen.queryByRole("button", { name: "Edit in chat" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Back to latest" }));

    expect(await screen.findByText("Latest body")).toBeInTheDocument();
    expect(screen.queryByText(/Viewing v1/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit in chat" })).toBeInTheDocument();
    expect(onEdit).not.toHaveBeenCalled();
  });

  it("keeps the plain version badge when the revisions endpoint 404s", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({ title: "Sprint report", kind: "markdown", content: "Latest body", version: 3 }),
    );
    // beforeEach already rejects the revisions read — the deployed-yesterday
    // backend. The header must degrade to exactly what it showed before.
    renderPane();

    expect(await screen.findByText("Document · v3")).toBeInTheDocument();
    await waitFor(() => expect(mockListRevisions).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Version history" })).not.toBeInTheDocument();
  });

  it("does not offer a picker for an artifact that has only ever had one version", async () => {
    mockGetArtifact.mockResolvedValue(artifact({ kind: "markdown", content: "Body", version: 1 }));
    mockListRevisions.mockResolvedValue([revisions[2]]);

    renderPane();

    await screen.findByText("Body");
    await waitFor(() => expect(mockListRevisions).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Version history" })).not.toBeInTheDocument();
    expect(screen.getByText("Document · v1")).toBeInTheDocument();
  });
});

describe("ArtifactPane — artifact switcher", () => {
  it("lists the session's artifacts and switches to the one picked", async () => {
    mockGetArtifact.mockResolvedValue(artifact({ kind: "markdown", content: "Body" }));
    mockListSessionArtifacts.mockResolvedValue([
      { id: "art-1", session_id: "session-1", title: "Agent usage by day", kind: "chart", version: 2, created_at: "2026-09-16T10:00:00Z", updated_at: "2026-09-16T10:00:00Z" },
      { id: "art-2", session_id: "session-1", title: "Sprint report", kind: "markdown", version: 1, created_at: "2026-09-16T09:00:00Z", updated_at: "2026-09-16T09:00:00Z" },
    ]);
    const onSwitchArtifact = vi.fn();

    renderPane({ sessionId: "session-1", onSwitchArtifact });

    const trigger = await screen.findByRole("button", { name: "Artifacts in this chat" });
    fireEvent.click(trigger);

    expect(screen.getByRole("menuitem", { name: /Agent usage by day/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: /Sprint report/ }));

    expect(onSwitchArtifact).toHaveBeenCalledWith("art-2");
    expect(mockListSessionArtifacts).toHaveBeenCalledWith("session-1");
  });

  it("leaves the title static when the session has nothing else to switch to", async () => {
    mockGetArtifact.mockResolvedValue(artifact({ kind: "markdown", content: "Body" }));
    mockListSessionArtifacts.mockResolvedValue([
      { id: "art-1", session_id: "session-1", title: "Agent usage by day", kind: "chart", version: 1, created_at: "", updated_at: "" },
    ]);

    renderPane({ sessionId: "session-1", onSwitchArtifact: vi.fn() });

    await screen.findByText("Agent usage by day");
    expect(screen.queryByRole("button", { name: "Artifacts in this chat" })).not.toBeInTheDocument();
  });
});

describe("ArtifactPane — updating hint", () => {
  it("shows the updating badge only while the run is writing to this artifact", async () => {
    mockGetArtifact.mockResolvedValue(artifact({ kind: "markdown", content: "Body" }));

    const { rerender } = renderPane({ isUpdating: false });
    await screen.findByText("Body");
    expect(screen.queryByText("Updating")).not.toBeInTheDocument();

    rerender(
      <I18nProvider locale="en" resources={RESOURCES}>
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <ArtifactPane artifactId="art-1" onClose={vi.fn()} isUpdating />
        </QueryClientProvider>
      </I18nProvider>,
    );

    expect(await screen.findByText("Updating")).toBeInTheDocument();
  });
});

describe("ArtifactPane — export", () => {
  it("downloads a table artifact as a properly quoted CSV, with no server call", async () => {
    mockGetArtifact.mockResolvedValue(
      artifact({
        title: "Open issues",
        kind: "table",
        content: JSON.stringify({
          columns: ["Issue", "Note"],
          rows: [["MUL-1", 'needs "review", today'], ["MUL-2", null]],
        }),
      }),
    );

    const blobs: Blob[] = [];
    const createObjectURL = vi.fn((blob: Blob) => {
      blobs.push(blob);
      return "blob:artifact";
    });
    const originalCreate = URL.createObjectURL;
    const originalRevoke = URL.revokeObjectURL;
    URL.createObjectURL = createObjectURL as unknown as typeof URL.createObjectURL;
    URL.revokeObjectURL = vi.fn();
    const clicked: HTMLAnchorElement[] = [];
    const clickSpy = vi
      .spyOn(HTMLAnchorElement.prototype, "click")
      .mockImplementation(function (this: HTMLAnchorElement) {
        clicked.push(this);
      });

    try {
      renderPane();
      fireEvent.click(await screen.findByRole("button", { name: "Download" }));

      expect(clicked[0]?.download).toBe("Open issues.csv");
      expect(blobs[0]?.type).toBe("text/csv;charset=utf-8");
      await expect(blobs[0]!.text()).resolves.toBe(
        'Issue,Note\r\nMUL-1,"needs ""review"", today"\r\nMUL-2,',
      );
    } finally {
      clickSpy.mockRestore();
      URL.createObjectURL = originalCreate;
      URL.revokeObjectURL = originalRevoke;
    }
  });
});
