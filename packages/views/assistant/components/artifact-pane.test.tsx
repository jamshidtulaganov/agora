import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import type { AssistantArtifact } from "@agora/core/types";
import { RESOURCES } from "../../locales";

const mockGetArtifact = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/assistant", () => ({
  assistantArtifactOptions: (id: string) => ({
    queryKey: ["assistant", "artifact", id],
    queryFn: () => mockGetArtifact(id),
    enabled: !!id,
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

function renderPane(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <ArtifactPane artifactId="art-1" onClose={onClose} />
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
});

afterEach(() => {
  cleanup();
});

describe("ArtifactPane — kind rendering", () => {
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
