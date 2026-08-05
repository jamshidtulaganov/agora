// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const state = vi.hoisted(() => ({
  role: "owner" as "owner" | "admin" | "member",
  query: {
    data: {
      workspace_id: "ws-1",
      mcp_config: { mcpServers: { fetch: { command: "uvx" } } },
    } as { workspace_id: string; mcp_config: unknown } | undefined,
    isLoading: false,
    isError: false,
    isFetching: false,
  },
  mutateAsync: vi.fn(),
  putCredential: vi.fn(),
  deleteCredential: vi.fn(),
  refetch: vi.fn(),
  credentials: [] as Array<{
    id: string;
    server_name: string;
    has_secret: boolean;
    last4: string;
    created_at: string;
    updated_at: string;
  }>,
}));

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const original = await importOriginal<typeof import("@tanstack/react-query")>();
  return {
    ...original,
    useQuery: (options: { queryKey?: string[] }) =>
      options.queryKey?.at(-1) === "mcp-credentials"
        ? { data: state.credentials, isLoading: false, isError: false }
        : { ...state.query, refetch: state.refetch },
  };
});

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@agora/core/permissions", () => ({
  useCurrentMember: () => ({ role: state.role, isLoading: false }),
}));

vi.mock("@agora/core/mcp", async (importOriginal) => {
  const original = await importOriginal<typeof import("@agora/core/mcp")>();
  return {
    ...original,
    workspaceMcpConfigOptions: vi.fn(() => ({ queryKey: ["workspaces", "ws-1", "mcp-config"] })),
    useUpdateWorkspaceMcpConfig: () => ({
      mutateAsync: state.mutateAsync,
      isPending: false,
    }),
    usePutMcpCredential: () => ({
      mutateAsync: state.putCredential,
      isPending: false,
    }),
    useDeleteMcpCredential: () => ({
      mutateAsync: state.deleteCredential,
      isPending: false,
    }),
  };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { WorkspaceMcpConfigSection } from "./workspace-mcp-config-section";

describe("WorkspaceMcpConfigSection", () => {
  beforeEach(() => {
    state.role = "owner";
    state.query = {
      data: {
        workspace_id: "ws-1",
        mcp_config: { mcpServers: { fetch: { command: "uvx" } } },
      },
      isLoading: false,
      isError: false,
      isFetching: false,
    };
    state.mutateAsync.mockReset();
    state.putCredential.mockReset();
    state.deleteCredential.mockReset();
    state.refetch.mockReset();
    state.credentials = [];
  });

  it("loads and counts the shared workspace servers", () => {
    render(<WorkspaceMcpConfigSection />);

    expect(screen.getByText("1 server")).toBeInTheDocument();
    expect(screen.getByLabelText(/workspace mcp\.json editor/i)).toHaveValue(
      JSON.stringify(state.query.data?.mcp_config, null, 2),
    );
    expect(screen.getByRole("button", { name: /save shared config/i })).toBeDisabled();
  });

  it("validates edits and saves the parsed JSON document", async () => {
    const user = userEvent.setup();
    state.mutateAsync.mockResolvedValue({
      workspace_id: "ws-1",
      mcp_config: { mcpServers: { linear: { type: "http", url: "https://mcp.linear.app/mcp" } } },
    });
    render(<WorkspaceMcpConfigSection />);

    const editor = screen.getByLabelText(/workspace mcp\.json editor/i);
    fireEvent.change(editor, { target: { value: "{ nope" } });
    expect(screen.getByText(/invalid json/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /save shared config/i })).toBeDisabled();

    const next = {
      mcpServers: {
        linear: { type: "http", url: "https://mcp.linear.app/mcp" },
      },
    };
    fireEvent.change(editor, { target: { value: JSON.stringify(next) } });
    await user.click(screen.getByRole("button", { name: /save shared config/i }));

    expect(state.mutateAsync).toHaveBeenCalledWith(next);
  });

  it("blocks editing and saving when the current config failed to load", async () => {
    const user = userEvent.setup();
    state.query = {
      data: undefined,
      isLoading: false,
      isError: true,
      isFetching: false,
    };
    render(<WorkspaceMcpConfigSection />);

    expect(screen.getByRole("textbox")).toBeDisabled();
    expect(screen.getByRole("button", { name: /save shared config/i })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: /retry/i }));
    expect(state.refetch).toHaveBeenCalledTimes(1);
  });

  it("seals auth for a saved remote workspace server without creating an agent override", async () => {
    const user = userEvent.setup();
    state.query.data = {
      workspace_id: "ws-1",
      mcp_config: {
        mcpServers: {
          linear: {
            type: "http",
            url: "https://mcp.linear.app/mcp",
            headers: { Authorization: "" },
          },
        },
      },
    };
    state.putCredential.mockResolvedValue({});
    render(<WorkspaceMcpConfigSection />);

    await user.type(screen.getByLabelText(/auth header value for linear/i), "Bearer secret");
    await user.click(screen.getByRole("button", { name: /seal token/i }));

    expect(state.putCredential).toHaveBeenCalledWith({
      serverName: "linear",
      data: { header_name: "Authorization", secret: "Bearer secret" },
    });
  });

  it("keeps workspace defaults read-only for regular members", () => {
    state.role = "member";
    render(<WorkspaceMcpConfigSection />);

    expect(screen.getByText(/managed by owners and admins/i)).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });
});
