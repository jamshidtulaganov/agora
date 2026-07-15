import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { ArtifactChecksPanel, ArtifactPreviewPanel } from "./artifact-runtime-panels";

const mocks = vi.hoisted(() => ({ getIssueArtifact: vi.fn() }));

vi.mock("@agora/core/api", () => ({ api: { getIssueArtifact: mocks.getIssueArtifact } }));

const HEAD = "b".repeat(40);

function artifactResponse() {
  return {
    run_id: "run-1",
    run_status: "running",
    ready: true,
    artifact: {
      id: "artifact-1",
      run_id: "run-1",
      step_id: "integration-1",
      step_key: "integrate",
      title: "Integration",
      kind: "integration",
      capability: "integration",
      canonical: true,
      repos: [{ repo: "agora", base_sha: "a".repeat(40), head_sha: HEAD, merge_status: "clean" }],
    },
    components: [],
    daemon_url: "http://127.0.0.1:9090",
    capabilities: { preview: "preview-token", checks: "checks-token" },
  };
}

function renderPanel(panel: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={client}>{panel}</QueryClientProvider>
    </I18nProvider>,
  );
}

describe("exact-head artifact runtime panels", () => {
  beforeEach(() => {
    mocks.getIssueArtifact.mockResolvedValue(artifactResponse());
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("runs checks with only the signed capability and artifact repository", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      artifact_id: "artifact-1",
      head_sha: HEAD,
      command: "pnpm test",
      exit_code: 0,
      passed: true,
      output: "all tests passed",
    }), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactChecksPanel issueId="issue-1" />);

    fireEvent.click(await screen.findByRole("button", { name: "Run checks" }));
    expect(await screen.findByText("Checks passed")).toBeInTheDocument();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://127.0.0.1:9090/artifact/checks");
    expect(JSON.parse(String(init.body))).toEqual({ capability: "checks-token", repo: "agora" });
    expect(String(init.body)).not.toContain("command");
    expect(String(init.body)).not.toContain("workdir");
  });

  it("starts a sandboxed preview without accepting a browser command", async () => {
    const fetchMock = vi.fn(async (url: string, _init?: RequestInit) => {
      const body = url.endsWith("/artifact/preview/status")
        ? { artifact_id: "artifact-1", running: false }
        : { artifact_id: "artifact-1", running: true, proxy_path: "/editor/local/3100/" };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    fireEvent.click(await screen.findByRole("button", { name: "Start preview" }));
    const frame = await screen.findByTitle("Integrated product preview");
    expect(frame).toHaveAttribute("src", "http://127.0.0.1:9090/editor/local/3100/");
    expect(frame).toHaveAttribute("sandbox");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-same-origin");

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:9090/artifact/preview",
      expect.any(Object),
    ));
    const startCall = fetchMock.mock.calls.find(([url]) => String(url).endsWith("/artifact/preview"));
    const body = JSON.parse(String((startCall?.[1] as RequestInit | undefined)?.body));
    expect(body).toEqual({ capability: "preview-token", repo: "agora" });
    expect(body).not.toHaveProperty("command");
    expect(body).not.toHaveProperty("workdir");
  });
});
