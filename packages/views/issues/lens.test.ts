import { createElement, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { NavigationProvider } from "../navigation";
import type { NavigationAdapter } from "../navigation/types";
import { LENS_QUERY_KEY, isLensRegistered, useLensParam } from "./lens";

function makeNav(overrides: Partial<NavigationAdapter> = {}): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/issues/issue-1",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p: string) => `https://app.agora.com${p}`,
    ...overrides,
  };
}

function makeWrapper(nav: NavigationAdapter) {
  return ({ children }: { children: ReactNode }) =>
    createElement(NavigationProvider, { value: nav, children });
}

describe("lens registry", () => {
  it("registers the default 'issue' lens", () => {
    expect(isLensRegistered("issue")).toBe(true);
  });

  it("registers the qa lens (phase D); other stage lenses stay unregistered", () => {
    expect(isLensRegistered("design")).toBe(false);
    expect(isLensRegistered("dev")).toBe(false);
    expect(isLensRegistered("qa")).toBe(true);
    expect(isLensRegistered("review")).toBe(false);
    expect(isLensRegistered("deploy")).toBe(false);
  });
});

describe("useLensParam", () => {
  it("defaults to the issue lens when there is no ?lens= param", () => {
    const nav = makeNav();
    const { result } = renderHook(() => useLensParam(), { wrapper: makeWrapper(nav) });
    expect(result.current.lens).toBe("issue");
  });

  it("falls back to issue for an unknown/unregistered ?lens= value", () => {
    const nav = makeNav({ searchParams: new URLSearchParams("lens=design") });
    const { result } = renderHook(() => useLensParam(), { wrapper: makeWrapper(nav) });
    expect(result.current.lens).toBe("issue");
  });

  it("recognizes the qa lens key from the URL (phase D)", () => {
    const nav = makeNav({ searchParams: new URLSearchParams("lens=qa") });
    const { result } = renderHook(() => useLensParam(), { wrapper: makeWrapper(nav) });
    expect(result.current.lens).toBe("qa");
  });

  it("falls back to issue for garbage ?lens= input", () => {
    const nav = makeNav({ searchParams: new URLSearchParams("lens=%3Cscript%3E") });
    const { result } = renderHook(() => useLensParam(), { wrapper: makeWrapper(nav) });
    expect(result.current.lens).toBe("issue");
  });

  it("recognizes a registered lens key from the URL", () => {
    const nav = makeNav({ searchParams: new URLSearchParams("lens=issue") });
    const { result } = renderHook(() => useLensParam(), { wrapper: makeWrapper(nav) });
    expect(result.current.lens).toBe("issue");
  });

  it("writes via navigation.replace (not push) and preserves other query params", () => {
    const nav = makeNav({
      pathname: "/acme/issues/issue-1",
      searchParams: new URLSearchParams("highlight=comment-1"),
    });
    const { result } = renderHook(() => useLensParam(), { wrapper: makeWrapper(nav) });

    act(() => {
      result.current.setLens("issue");
    });

    expect(nav.push).not.toHaveBeenCalled();
    expect(nav.replace).toHaveBeenCalledTimes(1);

    const calledWith = vi.mocked(nav.replace).mock.calls[0]![0];
    const [path, query] = calledWith.split("?");
    expect(path).toBe("/acme/issues/issue-1");

    const params = new URLSearchParams(query);
    expect(params.get("highlight")).toBe("comment-1");
    expect(params.get(LENS_QUERY_KEY)).toBe("issue");
  });
});
