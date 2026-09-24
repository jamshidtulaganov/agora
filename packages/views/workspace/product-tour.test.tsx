import type { ReactNode } from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import { useProductTourStore } from "@agora/core/onboarding";
import { RESOURCES } from "../locales";

const currentWorkspace = vi.hoisted(() => ({ value: { id: "ws-1" } as { id: string } | null }));
vi.mock("@agora/core/paths", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agora/core/paths")>()),
  useCurrentWorkspace: () => currentWorkspace.value,
}));

import { ProductTour } from "./product-tour";

beforeAll(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
});

// jsdom lays nothing out, so every element measures 0×0 — which the tour
// reads as "not on screen". Give the sidebar anchors a real box.
function addAnchors(keys: string[]) {
  keys.forEach((key, i) => {
    const el = document.createElement("div");
    el.setAttribute("data-nav-key", key);
    el.getBoundingClientRect = () =>
      ({ left: 8, top: 40 + i * 32, right: 200, bottom: 68 + i * 32, width: 192, height: 28, x: 8, y: 40 + i * 32, toJSON: () => ({}) }) as DOMRect;
    document.body.append(el);
  });
}

function renderTour() {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={RESOURCES}>{children}</I18nProvider>
  );
  return render(<ProductTour />, { wrapper });
}

beforeEach(() => {
  currentWorkspace.value = { id: "ws-1" };
  useProductTourStore.getState().stop();
});

afterEach(() => {
  document.querySelectorAll("[data-nav-key]").forEach((el) => el.remove());
});

describe("ProductTour", () => {
  it("renders nothing until the setup starts it for this workspace", () => {
    addAnchors(["workspace-switcher", "myIssues", "issues", "inbox", "assistant"]);
    renderTour();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    act(() => useProductTourStore.getState().start("ws-other"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("walks the sidebar stops in order and ends on the last", async () => {
    addAnchors(["workspace-switcher", "myIssues", "issues", "inbox", "assistant"]);
    act(() => useProductTourStore.getState().start("ws-1"));
    renderTour();

    expect(await screen.findByRole("dialog", { name: "Your workspaces" })).toBeInTheDocument();
    expect(screen.getByText("1 of 5")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByRole("dialog", { name: "My tasks" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("dialog", { name: "Your workspaces" })).toBeInTheDocument();

    for (let i = 0; i < 4; i += 1) {
      await userEvent.click(screen.getByRole("button", { name: "Next" }));
    }
    expect(screen.getByRole("dialog", { name: "Assistant" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Got it" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(useProductTourStore.getState().workspaceId).toBeNull();
  });

  it("skips a stop whose item is not on screen, and closes on Escape", async () => {
    addAnchors(["myIssues", "issues", "inbox", "assistant"]);
    act(() => useProductTourStore.getState().start("ws-1"));
    renderTour();

    expect(await screen.findByRole("dialog", { name: "My tasks" }, { timeout: 3000 })).toBeInTheDocument();
    expect(screen.getByText("1 of 4")).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("ends by itself when there is nothing to point at", async () => {
    act(() => useProductTourStore.getState().start("ws-1"));
    renderTour();
    await waitFor(() => expect(useProductTourStore.getState().workspaceId).toBeNull(), { timeout: 3000 });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
