import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { ArtifactChecksPanel, ArtifactPreviewPanel } from "./artifact-runtime-panels";

const mocks = vi.hoisted(() => ({
  getIssueArtifact: vi.fn(),
  getIssueQAPreviewURL: vi.fn(),
  isDesktopShell: vi.fn(() => false),
}));

vi.mock("@agora/core/api", () => ({
  api: {
    getIssueArtifact: mocks.getIssueArtifact,
    getIssueQAPreviewURL: mocks.getIssueQAPreviewURL,
  },
}));

vi.mock("../../platform", () => ({ isDesktopShell: mocks.isDesktopShell }));

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
    // Default: no standing dev server → fall through to the daemon preview
    // chain, so the existing daemon-based assertions below hold unchanged.
    mocks.getIssueQAPreviewURL.mockResolvedValue({ url: "", embeddable: false });
    // Default to the web shell so CSP embeddability is honored; the desktop
    // bypass is exercised explicitly in its own test.
    mocks.isDesktopShell.mockReturnValue(false);
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
        ? {
            artifact_id: "artifact-1",
            running: false,
            command: "pnpm run preview:mytrion",
            configuration_source: "project",
          }
        : {
            artifact_id: "artifact-1",
            running: true,
            ready: true,
            proxy_path: "/editor/local/3100/",
            command: "pnpm run preview:mytrion",
            configuration_source: "project",
          };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    expect(await screen.findByText("Project workflow")).toBeInTheDocument();
    expect(screen.getByText("pnpm run preview:mytrion")).toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: "Start preview" }));
    const frame = await screen.findByTitle("Integrated product preview");
    expect(frame).toHaveAttribute("src", "http://127.0.0.1:9090/editor/local/3100/");
    expect(frame).toHaveAttribute("sandbox");
    expect(frame.getAttribute("sandbox")).toContain("allow-same-origin");
    expect(frame.parentElement).toHaveClass("h-full", "max-h-[48rem]", "w-full", "max-w-none");
    expect(screen.getByRole("region", { name: "Preview" })).toHaveClass("h-[calc(100dvh-9.5rem)]");

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

  it("keeps same-origin cloud proxy previews in the strict sandbox", async () => {
    const response = artifactResponse();
    response.daemon_url = "/api/daemon/runtime-1";
    mocks.getIssueArtifact.mockResolvedValue(response);
    const fetchMock = vi.fn(async (url: string) => {
      const body = url.endsWith("/artifact/preview/status")
        ? { artifact_id: "artifact-1", running: false }
        : {
            artifact_id: "artifact-1",
            running: true,
            ready: true,
            proxy_path: "/editor/local/3100/",
          };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    fireEvent.click(await screen.findByRole("button", { name: "Start preview" }));

    const frame = await screen.findByTitle("Integrated product preview");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-same-origin");
  });

  it("shows command output until the preview port is ready", async () => {
    const fetchMock = vi.fn(async (url: string) => {
      const body = url.endsWith("/artifact/preview/status")
        ? { artifact_id: "artifact-1", running: false, configuration_source: "project_repository" }
        : {
            artifact_id: "artifact-1",
            running: true,
            starting: true,
            ready: false,
            command: "pnpm --filter mytrion dev",
            configuration_source: "project_repository",
            log: "VITE starting development server…",
          };
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    fireEvent.click(await screen.findByRole("button", { name: "Start preview" }));

    expect(await screen.findByText("Repository override")).toBeInTheDocument();
    expect(screen.getAllByText("pnpm --filter mytrion dev")).toHaveLength(2);
    expect(screen.getByText("VITE starting development server…")).toBeInTheDocument();
    expect(screen.queryByTitle("Integrated product preview")).not.toBeInTheDocument();
  });

  it("embeds a standing dev server directly instead of the daemon Docker preview", async () => {
    mocks.getIssueQAPreviewURL.mockResolvedValue({
      url: "https://agora-cs.sdteam.uz",
      embeddable: true,
    });
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    // The dev server's host is shown and its URL is framed…
    expect(await screen.findByText("agora-cs.sdteam.uz")).toBeInTheDocument();
    const frame = screen.getByTitle("Integrated product preview") as HTMLIFrameElement;
    expect(frame.src).toBe("https://agora-cs.sdteam.uz/");
    // …and there is no Start/Stop chrome, because a standing server needs no build.
    expect(screen.queryByRole("button", { name: "Start preview" })).not.toBeInTheDocument();
    // The daemon preview endpoints are never hit in this mode.
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("falls back to the daemon preview when the dev server is not embeddable", async () => {
    mocks.getIssueQAPreviewURL.mockResolvedValue({
      url: "https://agora-cs.sdteam.uz",
      embeddable: false,
    });
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ artifact_id: "artifact-1", running: false }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    // A non-embeddable dev server must NOT be framed; the daemon Start button shows.
    expect(await screen.findByRole("button", { name: "Start preview" })).toBeInTheDocument();
    expect(screen.queryByText("agora-cs.sdteam.uz")).not.toBeInTheDocument();
  });

  it("frames a non-embeddable dev server anyway in the desktop shell", async () => {
    // Desktop runs webSecurity:false, so a scoped frame-ancestors CSP is not
    // enforced — embed even though the server-side probe says non-embeddable.
    mocks.isDesktopShell.mockReturnValue(true);
    mocks.getIssueQAPreviewURL.mockResolvedValue({
      url: "https://agora-cs.sdteam.uz",
      embeddable: false,
    });
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    renderPanel(<ArtifactPreviewPanel issueId="issue-1" />);

    const frame = (await screen.findByTitle("Integrated product preview")) as HTMLIFrameElement;
    expect(frame.src).toBe("https://agora-cs.sdteam.uz/");
    expect(screen.queryByRole("button", { name: "Start preview" })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
