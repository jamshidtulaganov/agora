import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SystemNotificationPayload } from "@agora/core/platform";
import enInbox from "../locales/en/inbox.json";
import {
  NotificationToastBridge,
  playNotificationChime,
} from "./notification-toast-bridge";

const state = vi.hoisted(() => ({
  handler: null as ((payload: SystemNotificationPayload) => void) | null,
  push: vi.fn(),
  toast: vi.fn(),
}));

vi.mock("@agora/core/platform", () => ({
  registerForegroundSystemNotificationHandler: (
    handler: ((payload: SystemNotificationPayload) => void) | null,
  ) => {
    state.handler = handler;
  },
}));

vi.mock("@agora/core/paths", () => ({
  paths: {
    workspace: (slug: string) => ({ inbox: () => `/${slug}/inbox` }),
  },
}));

vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: state.push }),
}));

vi.mock("../i18n", () => ({
  useT: () => ({
    t: (selector: (dictionary: typeof enInbox) => string) => selector(enInbox),
  }),
}));

vi.mock("sonner", () => ({ toast: state.toast }));

const payload: SystemNotificationPayload = {
  slug: "acme",
  itemId: "item-1",
  issueKey: "issue-1",
  title: "Mentioned you",
  body: "Please take a look",
};

describe("NotificationToastBridge", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    state.push.mockReset();
    state.toast.mockReset();
    state.handler = null;
  });

  afterEach(() => {
    vi.useRealTimers();
    delete (window as unknown as Record<string, unknown>).AudioContext;
  });

  it("shows a focused-app toast and opens its source workspace", () => {
    render(<NotificationToastBridge />);

    act(() => state.handler?.(payload));

    expect(state.toast).toHaveBeenCalledWith(
      "Mentioned you",
      expect.objectContaining({
        id: "inbox-item-1",
        description: "Please take a look",
      }),
    );
    const options = state.toast.mock.calls[0]?.[1] as {
      action?: { onClick: () => void };
    };
    options.action?.onClick();
    expect(state.push).toHaveBeenCalledWith("/acme/inbox?issue=issue-1");
  });

  it("deduplicates replayed events so they do not ring twice", () => {
    render(<NotificationToastBridge />);

    act(() => {
      state.handler?.(payload);
      state.handler?.(payload);
    });

    expect(state.toast).toHaveBeenCalledTimes(1);
  });

  it("plays a two-note chime", () => {
    const start = vi.fn();
    const createOscillator = vi.fn(() => ({
      type: "sine",
      frequency: { setValueAtTime: vi.fn() },
      connect: vi.fn(),
      start,
      stop: vi.fn(),
    }));
    const createGain = vi.fn(() => ({
      gain: {
        setValueAtTime: vi.fn(),
        exponentialRampToValueAtTime: vi.fn(),
      },
      connect: vi.fn(),
    }));
    class FakeAudioContext {
      currentTime = 1;
      destination = {};
      resume = vi.fn().mockResolvedValue(undefined);
      createOscillator = createOscillator;
      createGain = createGain;
    }
    Object.defineProperty(window, "AudioContext", {
      configurable: true,
      value: FakeAudioContext,
    });

    playNotificationChime();

    expect(createOscillator).toHaveBeenCalledTimes(2);
    expect(createGain).toHaveBeenCalledTimes(2);
    expect(start).toHaveBeenCalledTimes(2);
  });
});
