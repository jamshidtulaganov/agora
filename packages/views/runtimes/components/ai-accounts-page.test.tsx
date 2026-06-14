// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import type { AgentRuntime } from "@agora/core/types";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

// useWorkspaceId reads workspace context in production; the page only needs a
// stable id to key the query, so a constant is enough here.
vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

// The WS subscription is a live-refresh nicety; make it a no-op in tests.
vi.mock("@agora/core/realtime", () => ({
  useWSEvent: () => {},
}));

vi.mock("@agora/ui/lib/clipboard", () => ({
  copyText: vi.fn().mockResolvedValue(true),
}));

// Drive the runtime list through a mocked useQuery so each test controls the
// exact metadata the page derives from.
vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: vi.fn(() => ({ data: [], isLoading: false })),
  };
});

import { useQuery } from "@tanstack/react-query";
import {
  AiAccountsPage,
  deriveProviderAccounts,
} from "./ai-accounts-page";

const mockedUseQuery = vi.mocked(useQuery);

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Test Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-me",
    visibility: "private",
    last_seen_at: new Date().toISOString(),
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function setRuntimes(runtimes: AgentRuntime[], isLoading = false) {
  mockedUseQuery.mockImplementation(
    (() => ({ data: runtimes, isLoading })) as unknown as typeof useQuery,
  );
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <AiAccountsPage />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("AiAccountsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setRuntimes([]);
  });

  it("renders a card for every known provider, even with no runtimes", () => {
    setRuntimes([]);
    renderPage();
    for (const provider of ["claude", "codex", "gemini", "antigravity"]) {
      expect(screen.getByTestId(`ai-account-${provider}`)).toBeInTheDocument();
    }
  });

  it("shows the connected state with email and plan from runtime metadata", () => {
    setRuntimes([
      makeRuntime({
        id: "rt-claude",
        provider: "claude",
        status: "online",
        metadata: {
          auth_state: "logged_in",
          account_email: "dev@anthropic.com",
          account_plan: "Max",
        },
      }),
    ]);
    renderPage();

    const card = screen.getByTestId("ai-account-claude");
    expect(card.getAttribute("data-state")).toBe("connected");
    expect(within(card).getByText("Connected")).toBeInTheDocument();
    expect(within(card).getByText("dev@anthropic.com")).toBeInTheDocument();
    expect(within(card).getByText("Max")).toBeInTheDocument();
  });

  it("shows not-connected when auth_state is logged_out", () => {
    setRuntimes([
      makeRuntime({
        id: "rt-codex",
        provider: "codex",
        status: "online",
        metadata: { auth_state: "logged_out" },
      }),
    ]);
    renderPage();

    const card = screen.getByTestId("ai-account-codex");
    expect(card.getAttribute("data-state")).toBe("not_connected");
    expect(within(card).getByText("Not connected")).toBeInTheDocument();
    // Connect guidance is shown for non-connected providers.
    expect(within(card).getByText("How to connect")).toBeInTheDocument();
  });

  it("degrades gracefully when metadata is absent (older runtime) -> unknown", () => {
    setRuntimes([
      makeRuntime({
        id: "rt-gemini",
        provider: "gemini",
        status: "online",
        metadata: {}, // no auth keys at all
      }),
    ]);
    renderPage();

    const card = screen.getByTestId("ai-account-gemini");
    expect(card.getAttribute("data-state")).toBe("unknown");
    expect(within(card).getByText("Unknown")).toBeInTheDocument();
  });

  it("shows offline (not stale auth) when no runtime for the provider is online", () => {
    setRuntimes([
      makeRuntime({
        id: "rt-claude-off",
        provider: "claude",
        status: "offline",
        last_seen_at: new Date(Date.now() - 60 * 60 * 1000).toISOString(),
        metadata: { auth_state: "logged_in", account_email: "x@y.com" },
      }),
    ]);
    renderPage();

    const card = screen.getByTestId("ai-account-claude");
    // Offline wins over a stale logged_in metadata from a runtime that's gone.
    expect(card.getAttribute("data-state")).toBe("offline");
    expect(within(card).getByText("Offline")).toBeInTheDocument();
  });

  it("renders the loading skeleton while the query is pending", () => {
    setRuntimes([], true);
    renderPage();
    expect(screen.getByTestId("ai-accounts-loading")).toBeInTheDocument();
  });
});

describe("deriveProviderAccounts", () => {
  const now = Date.now();

  it("returns one entry per known provider in stable order", () => {
    const accounts = deriveProviderAccounts([], now);
    expect(accounts.map((a) => a.provider)).toEqual([
      "claude",
      "codex",
      "gemini",
      "antigravity",
    ]);
    for (const a of accounts) {
      expect(a.runtimeCount).toBe(0);
      expect(a.health).toBe("none");
      expect(a.authState).toBe("unknown");
      expect(a.online).toBe(false);
    }
  });

  it("reads auth metadata from the chosen runtime", () => {
    const accounts = deriveProviderAccounts(
      [
        makeRuntime({
          provider: "codex",
          status: "online",
          metadata: {
            auth_state: "logged_in",
            account_email: "a@b.com",
            account_plan: "Team",
          },
        }),
      ],
      now,
    );
    const codex = accounts.find((a) => a.provider === "codex")!;
    expect(codex.online).toBe(true);
    expect(codex.authState).toBe("logged_in");
    expect(codex.email).toBe("a@b.com");
    expect(codex.plan).toBe("Team");
    expect(codex.runtimeCount).toBe(1);
  });

  it("prefers a logged_in runtime when multiple share the best health", () => {
    const accounts = deriveProviderAccounts(
      [
        makeRuntime({
          id: "a",
          provider: "claude",
          status: "online",
          metadata: { auth_state: "unknown" },
        }),
        makeRuntime({
          id: "b",
          provider: "claude",
          status: "online",
          metadata: { auth_state: "logged_in", account_email: "win@x.com" },
        }),
      ],
      now,
    );
    const claude = accounts.find((a) => a.provider === "claude")!;
    expect(claude.authState).toBe("logged_in");
    expect(claude.email).toBe("win@x.com");
    expect(claude.runtimeCount).toBe(2);
  });

  it("ignores malformed metadata values defensively", () => {
    const accounts = deriveProviderAccounts(
      [
        makeRuntime({
          provider: "gemini",
          status: "online",
          metadata: {
            auth_state: 42 as unknown as string, // wrong type
            account_email: "", // empty -> null
          },
        }),
      ],
      now,
    );
    const gemini = accounts.find((a) => a.provider === "gemini")!;
    expect(gemini.authState).toBe("unknown");
    expect(gemini.email).toBeNull();
  });
});
