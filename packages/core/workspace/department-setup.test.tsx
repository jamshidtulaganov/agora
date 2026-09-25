/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { EMPTY_WORKSPACE } from "../api/schemas";
import type { Workspace } from "../types";
import {
  readDepartmentSetup,
  readTeamSidebar,
  useSetDepartmentSetup,
  useUpdateTeamSidebar,
} from "./department-setup";
import { workspaceKeys } from "./queries";
import { useSetupPromptStore } from "./setup-prompt-store";

describe("readTeamSidebar", () => {
  it("reads the hidden keys", () => {
    expect(readTeamSidebar({ team_sidebar: { hidden: ["agents", "usage"], updated_by: "u" } })).toEqual([
      "agents",
      "usage",
    ]);
  });

  it("treats an empty list as a real choice (show everything)", () => {
    expect(readTeamSidebar({ team_sidebar: { hidden: [] } })).toEqual([]);
  });

  it("drops non-string entries instead of discarding the whole list", () => {
    expect(readTeamSidebar({ team_sidebar: { hidden: ["agents", 3, null, "mcp"] } })).toEqual([
      "agents",
      "mcp",
    ]);
  });

  it.each([
    ["no settings", undefined],
    ["null settings", null],
    ["an array", ["team_sidebar"]],
    ["a string", "team_sidebar"],
    ["no key", { other: true }],
    ["a null value", { team_sidebar: null }],
    ["a string value", { team_sidebar: "agents" }],
    ["a missing hidden", { team_sidebar: { updated_by: "u" } }],
    ["a non-array hidden", { team_sidebar: { hidden: "agents" } }],
    ["a null hidden", { team_sidebar: { hidden: null } }],
  ])("returns null for %s", (_label, settings) => {
    expect(readTeamSidebar(settings)).toBeNull();
  });
});

describe("readDepartmentSetup", () => {
  it.each(["done", "skipped"] as const)("reads status %s", (status) => {
    expect(readDepartmentSetup({ department_setup: { status, by: "u", at: "2026-09-25T00:00:00Z" } })).toEqual({
      status,
    });
  });

  it.each([
    ["no settings", undefined],
    ["null settings", null],
    ["no key", {}],
    ["a null value", { department_setup: null }],
    ["a string value", { department_setup: "done" }],
    ["a missing status", { department_setup: { by: "u" } }],
    ["a non-string status", { department_setup: { status: 1 } }],
    ["an unknown status", { department_setup: { status: "in_progress" } }],
  ])("returns null for %s", (_label, settings) => {
    expect(readDepartmentSetup(settings)).toBeNull();
  });
});

const WS: Workspace = {
  id: "ws-1",
  name: "Collections",
  slug: "collections",
  description: null,
  context: null,
  settings: { labs: { keep: true } },
  repos: [],
  issue_prefix: "COL",
  avatar_url: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function cachedSettings(qc: QueryClient): Record<string, unknown> | undefined {
  return qc.getQueryData<Workspace[]>(workspaceKeys.list())?.find((w) => w.id === WS.id)?.settings;
}

describe("department setup mutations", () => {
  let qc: QueryClient;
  const api = {
    updateTeamSidebar: vi.fn(),
    setDepartmentSetup: vi.fn(),
    listWorkspaces: vi.fn(),
  };

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    qc.setQueryData(workspaceKeys.list(), [WS]);
    for (const fn of Object.values(api)) fn.mockReset();
    api.listWorkspaces.mockResolvedValue([WS]);
    setApiInstance(api as unknown as ApiClient);
  });

  afterEach(() => qc.clear());

  it("shows the team sidebar before the server answers and keeps sibling settings", async () => {
    const pending = deferred<Workspace>();
    api.updateTeamSidebar.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useUpdateTeamSidebar(), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ workspaceId: WS.id, hidden: ["agents"] }));

    await waitFor(() =>
      expect(cachedSettings(qc)).toEqual({ labs: { keep: true }, team_sidebar: { hidden: ["agents"] } }),
    );
    expect(api.updateTeamSidebar).toHaveBeenCalledWith(WS.id, ["agents"]);
    pending.resolve({ ...WS, settings: { ...WS.settings, team_sidebar: { hidden: ["agents"] } } });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  it("rolls the team sidebar back when the save fails", async () => {
    api.updateTeamSidebar.mockRejectedValue(new Error("forbidden"));
    const { result } = renderHook(() => useUpdateTeamSidebar(), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ workspaceId: WS.id, hidden: ["agents"] }));

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(cachedSettings(qc)).toEqual({ labs: { keep: true } });
  });

  it("keeps the optimistic value when the response drifts to the empty sentinel", async () => {
    api.updateTeamSidebar.mockResolvedValue(EMPTY_WORKSPACE);
    // The settle refetch would overwrite the cache; hold it so the assertion
    // sees what onSuccess left behind.
    api.listWorkspaces.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useUpdateTeamSidebar(), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ workspaceId: WS.id, hidden: ["usage"] }));

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list());
    expect(list).toHaveLength(1);
    expect(list?.[0]?.id).toBe(WS.id);
    expect(cachedSettings(qc)).toMatchObject({ team_sidebar: { hidden: ["usage"] } });
  });

  it("records the setup status optimistically and rolls back on failure", async () => {
    const pending = deferred<Workspace>();
    api.setDepartmentSetup.mockReturnValue(pending.promise);
    const { result } = renderHook(() => useSetDepartmentSetup(), { wrapper: createWrapper(qc) });

    act(() => result.current.mutate({ workspaceId: WS.id, status: "skipped" }));

    await waitFor(() => expect(readDepartmentSetup(cachedSettings(qc))).toEqual({ status: "skipped" }));
    expect(api.setDepartmentSetup).toHaveBeenCalledWith(WS.id, "skipped");

    pending.reject(new Error("offline"));
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(readDepartmentSetup(cachedSettings(qc))).toBeNull();
  });
});

describe("useSetupPromptStore", () => {
  it("remembers a dismissal per workspace and ignores repeats", () => {
    useSetupPromptStore.getState().dismiss("ws-1");
    const after = useSetupPromptStore.getState().dismissed;
    useSetupPromptStore.getState().dismiss("ws-1");
    expect(useSetupPromptStore.getState().dismissed).toBe(after);
    expect(after).toEqual({ "ws-1": true });
    expect(after["ws-2"]).toBeUndefined();
  });
});
