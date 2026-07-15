import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enIssues from "../../locales/en/issues.json";
import { QALiveBrowser } from "./qa-live-browser";

// QALiveBrowser is the QA lens's live-testing bay — signal-driven, not
// default-on (see the component's own comment). These tests cover the
// mount-gate itself: `active=false` must fire ZERO of the browser/
// preview requests (the bug this whole rework exists to fix — opening the QA
// lens used to auto-connect a CDP Chromium and auto-boot a dev server as a
// side effect of merely mounting this component) and render only the compact
// idle card; `active=true` fires the requests and renders the real pane.

const apiMocks = vi.hoisted(() => ({
  getIssueBrowser: vi.fn(),
  getIssueQAPreviewURL: vi.fn(),
}));

vi.mock("@agora/core/api", () => ({ api: apiMocks }));
vi.mock("../../issues/components/editor-browser-pane", () => ({
  EditorBrowserPane: () => <div data-testid="editor-browser-pane" />,
}));

function renderBrowser(props: Partial<Parameters<typeof QALiveBrowser>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onOpen = vi.fn();
  const onCollapse = vi.fn();
  const utils = render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <QueryClientProvider client={qc}>
        <QALiveBrowser
          issueId="issue-1"
          active={false}
          running={false}
          onOpen={onOpen}
          onCollapse={onCollapse}
          {...props}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...utils, onOpen, onCollapse };
}

describe("QALiveBrowser", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getIssueBrowser.mockResolvedValue({ mode: "self-host", daemon_url: "http://127.0.0.1:9" });
    apiMocks.getIssueQAPreviewURL.mockResolvedValue({ url: "https://qa.example.test" });
  });

  it("active=false: renders the compact idle card and fires zero network requests", async () => {
    renderBrowser({ active: false });

    expect(await screen.findByText("Most QA doesn't need a browser — open this to watch or drive the running app.")).toBeInTheDocument();
    expect(screen.queryByTestId("editor-browser-pane")).not.toBeInTheDocument();

    // No browser/preview call should ever fire while idle — this is
    // the exact regression (auto-connect CDP + auto-boot a dev server on
    // mere mount) the mount-gate exists to prevent.
    expect(apiMocks.getIssueBrowser).not.toHaveBeenCalled();
    expect(apiMocks.getIssueQAPreviewURL).not.toHaveBeenCalled();
  });

  it('active=false clicking "Open live testing" calls onOpen', async () => {
    const { onOpen } = renderBrowser({ active: false });

    fireEvent.click(await screen.findByRole("button", { name: "Open live testing" }));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("active=false + running: surfaces the live pulse on the idle card", async () => {
    renderBrowser({ active: false, running: true });

    expect(await screen.findByText("Live")).toBeInTheDocument();
  });

  it("active=true: fetches browser/preview and mounts the configured QA target", async () => {
    renderBrowser({ active: true });

    expect(await screen.findByTestId("editor-browser-pane")).toBeInTheDocument();
    expect(apiMocks.getIssueBrowser).toHaveBeenCalledWith("issue-1");
    expect(apiMocks.getIssueQAPreviewURL).toHaveBeenCalledWith("issue-1");
  });

  it("active=true: the collapse control calls onCollapse", async () => {
    const { onCollapse } = renderBrowser({ active: true });

    await screen.findByTestId("editor-browser-pane");
    fireEvent.click(screen.getByRole("button", { name: "Collapse live testing" }));
    expect(onCollapse).toHaveBeenCalledTimes(1);
  });
});
