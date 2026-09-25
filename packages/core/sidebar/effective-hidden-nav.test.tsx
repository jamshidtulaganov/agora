/**
 * @vitest-environment jsdom
 */
import { beforeEach, describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import type { ApiClient } from "../api/client";
import { createAuthStore, registerAuthStore } from "../auth";
import type { MemberWithUser, StorageAdapter, User } from "../types";
import { workspaceKeys } from "../workspace/queries";
import {
  effectiveHiddenNav,
  useEffectiveHiddenNav,
  usesTeamSidebar,
  type EffectiveHiddenNavInput,
} from "./effective-hidden-nav";

const OWN = ["usage"];
const TEAM = ["agents", "squads"];

describe("effectiveHiddenNav", () => {
  // customized × isAdmin × team sidebar present — every combination.
  const cases: Array<[boolean, boolean, string[] | null, string[]]> = [
    [false, false, TEAM, TEAM],
    [false, false, null, OWN],
    [false, true, TEAM, OWN],
    [false, true, null, OWN],
    [true, false, TEAM, OWN],
    [true, false, null, OWN],
    [true, true, TEAM, OWN],
    [true, true, null, OWN],
  ];

  it.each(cases)(
    "customized=%s admin=%s team=%j → %j",
    (customized, isAdmin, teamHidden, expected) => {
      const input: EffectiveHiddenNavInput = { userHidden: OWN, customized, isAdmin, teamHidden };
      // Same reference, not a copy: memoized consumers depend on it.
      expect(effectiveHiddenNav(input)).toBe(expected);
      expect(usesTeamSidebar(input)).toBe(expected === TEAM);
    },
  );

  it("applies an empty team sidebar (show everything) to a member", () => {
    const empty: string[] = [];
    expect(
      effectiveHiddenNav({ userHidden: OWN, customized: false, isAdmin: false, teamHidden: empty }),
    ).toBe(empty);
  });
});

function user(overrides: Partial<User> = {}): User {
  return {
    id: "user-1",
    name: "Dilnoza",
    email: "dilnoza@example.com",
    avatar_url: null,
    onboarded_at: "2026-01-01T00:00:00Z",
    onboarding_questionnaire: {},
    starter_content_state: null,
    language: null,
    profile_description: "",
    timezone: null,
    hidden_nav: OWN,
    hidden_nav_customized: false,
    created_at: "",
    updated_at: "",
    ...overrides,
  };
}

function member(role: MemberWithUser["role"]): MemberWithUser {
  return {
    id: "m-1",
    workspace_id: "ws-1",
    user_id: "user-1",
    role,
    created_at: "",
    name: "Dilnoza",
    email: "dilnoza@example.com",
    avatar_url: null,
  };
}

const memoryStorage: StorageAdapter = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
};
const authStore = createAuthStore({ api: {} as ApiClient, storage: memoryStorage });
registerAuthStore(authStore);

describe("useEffectiveHiddenNav", () => {
  let qc: QueryClient;
  const workspace = { id: "ws-1", settings: { team_sidebar: { hidden: TEAM } } };

  function wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  }

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    authStore.setState({ user: user() });
  });

  it("gives a member who never customized the team's list", () => {
    qc.setQueryData(workspaceKeys.members("ws-1"), [member("member")]);
    const { result } = renderHook(() => useEffectiveHiddenNav(workspace), { wrapper });
    expect(result.current.hidden).toEqual(TEAM);
    expect(result.current.fromTeam).toBe(true);
  });

  it("gives an admin their own list", () => {
    qc.setQueryData(workspaceKeys.members("ws-1"), [member("admin")]);
    const { result } = renderHook(() => useEffectiveHiddenNav(workspace), { wrapper });
    expect(result.current.hidden).toBe(OWN);
    expect(result.current.fromTeam).toBe(false);
  });

  it("gives a member who customized their own list", () => {
    authStore.setState({ user: user({ hidden_nav_customized: true }) });
    qc.setQueryData(workspaceKeys.members("ws-1"), [member("member")]);
    const { result } = renderHook(() => useEffectiveHiddenNav(workspace), { wrapper });
    expect(result.current.hidden).toBe(OWN);
  });

  it("falls back to the person's own list outside a workspace", () => {
    const { result } = renderHook(() => useEffectiveHiddenNav(null), { wrapper });
    expect(result.current).toEqual({ hidden: OWN, fromTeam: false });
  });

  it("returns the same object across renders", () => {
    qc.setQueryData(workspaceKeys.members("ws-1"), [member("member")]);
    const { result, rerender } = renderHook(() => useEffectiveHiddenNav(workspace), { wrapper });
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
    expect(result.current.hidden).toBe(first.hidden);
  });
});
