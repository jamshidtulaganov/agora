import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import type { AssistantMessage } from "@agora/core/types";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import { RESOURCES } from "../../locales";
import { MessageList } from "./message-list";

const writeText = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));

function renderList(
  messages: AssistantMessage[],
  onOpenArtifact?: (id: string) => void,
  onRegenerate?: () => void,
) {
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/assistant",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p) => p,
  };
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <NavigationProvider value={nav}>
        <MessageList
          messages={messages}
          onOpenArtifact={onOpenArtifact}
          onRegenerate={onRegenerate}
        />
      </NavigationProvider>
    </I18nProvider>,
  );
}

function toolMessage(overrides: Partial<AssistantMessage> & { id: string }): AssistantMessage {
  return {
    session_id: "session-1",
    role: "tool",
    content: "{}",
    created_at: "2026-09-16T10:00:00Z",
    ...overrides,
  } as AssistantMessage;
}

beforeEach(() => {
  writeText.mockClear();
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
});

afterEach(() => {
  cleanup();
});

describe("MessageList — artifact cards", () => {
  it("renders an artifact card for a create_artifact tool row", async () => {
    const onOpen = vi.fn();
    renderList(
      [
        toolMessage({
          id: "m1",
          tool_name: "create_artifact",
          tool_result: {
            artifact_id: "art-1",
            title: "Agent usage by day",
            kind: "chart",
            version: 1,
          },
        }),
      ],
      onOpen,
    );

    const card = screen.getByRole("button", { name: /Agent usage by day/ });
    expect(card).toBeInTheDocument();
    // v1 carries no badge — the version only earns pixels once it changed.
    expect(screen.queryByText("v1")).not.toBeInTheDocument();

    await userEvent.click(card);
    expect(onOpen).toHaveBeenCalledWith("art-1");
  });

  it("shows a version badge once update_artifact has bumped it", () => {
    renderList(
      [
        toolMessage({
          id: "m1",
          tool_name: "update_artifact",
          tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 3 },
        }),
      ],
      vi.fn(),
    );

    expect(screen.getByText("v3")).toBeInTheDocument();
  });

  it("falls back to the plain tool chip when the tool_result shape is unrecognised", () => {
    // Exactly what an older/newer server that renamed the field ships.
    renderList(
      [
        toolMessage({
          id: "m1",
          tool_name: "create_artifact",
          tool_result: { id: "art-1", title: "Sprint report" },
        }),
      ],
      vi.fn(),
    );

    expect(screen.queryByRole("button", { name: /Sprint report/ })).not.toBeInTheDocument();
    // The generic chip humanizes the tool name and summarizes the result.
    expect(screen.getByText("create artifact")).toBeInTheDocument();
    expect(screen.getByText("Sprint report")).toBeInTheDocument();
  });

  it("renders the card without a click target when no open handler is provided", () => {
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown" },
      }),
    ]);

    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.getByText("Sprint report")).toBeInTheDocument();
  });
});

describe("MessageList — message actions", () => {
  it("copies a user message verbatim", async () => {
    renderList([
      {
        id: "m1",
        session_id: "session-1",
        role: "user",
        content: "What's on my plate?",
        created_at: "2026-09-16T10:00:00Z",
      },
    ]);

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledWith("What's on my plate?");
    // The label flips to "Copied" as the confirmation flash.
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
    });
  });

  it("copies the assistant's RAW markdown, not the rendered text", async () => {
    const markdown = "## Plan\n\n- **one**\n- two";
    renderList([
      {
        id: "m1",
        session_id: "session-1",
        role: "assistant",
        content: markdown,
        created_at: "2026-09-16T10:00:00Z",
      },
    ]);

    await userEvent.click(screen.getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledWith(markdown);
  });

  it("gives a pure tool-call assistant turn no copy action (nothing is rendered)", () => {
    renderList([
      {
        id: "m1",
        session_id: "session-1",
        role: "assistant",
        content: "",
        created_at: "2026-09-16T10:00:00Z",
      },
    ]);

    expect(screen.queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
  });
});

// A conversation people come back to spans days; without a divider yesterday's
// answer reads as part of today's.
describe("MessageList — day separators", () => {
  const atOffset = (days: number, hour = 10): string => {
    const date = new Date();
    date.setDate(date.getDate() - days);
    date.setHours(hour, 0, 0, 0);
    return date.toISOString();
  };

  const chat = (id: string, created_at: string): AssistantMessage =>
    ({
      id,
      session_id: "session-1",
      role: "user",
      content: `msg ${id}`,
      created_at,
    }) as AssistantMessage;

  it("never puts a divider above the first message", () => {
    renderList([chat("m1", atOffset(0))]);

    expect(screen.queryByRole("separator")).not.toBeInTheDocument();
  });

  it("puts no divider between two messages from the same day", () => {
    renderList([chat("m1", atOffset(0, 9)), chat("m2", atOffset(0, 21))]);

    expect(screen.queryByRole("separator")).not.toBeInTheDocument();
  });

  it("names today and yesterday, one divider per change of day", () => {
    renderList([
      chat("m1", atOffset(2)),
      chat("m2", atOffset(1)),
      chat("m3", atOffset(1, 18)),
      chat("m4", atOffset(0)),
    ]);

    const separators = screen.getAllByRole("separator");
    expect(separators).toHaveLength(2);
    expect(separators[0]).toHaveAccessibleName("Yesterday");
    expect(separators[1]).toHaveAccessibleName("Today");
  });

  it("degrades quietly when a timestamp is unusable", () => {
    renderList([chat("m1", atOffset(1)), chat("m2", "")]);

    expect(screen.queryByRole("separator")).not.toBeInTheDocument();
  });
});

describe("MessageList — regenerate", () => {
  const assistantRow = (id: string, content: string): AssistantMessage =>
    ({
      id,
      session_id: "session-1",
      role: "assistant",
      content,
      created_at: "2026-09-16T10:00:00Z",
    }) as AssistantMessage;

  it("offers the action on the last assistant row only", async () => {
    const onRegenerate = vi.fn();
    renderList([assistantRow("m1", "first"), assistantRow("m2", "second")], undefined, onRegenerate);

    const buttons = screen.getAllByRole("button", { name: "Regenerate" });
    expect(buttons).toHaveLength(1);

    await userEvent.click(buttons[0]!);
    expect(onRegenerate).toHaveBeenCalledTimes(1);
  });

  it("skips a trailing pure tool-call turn, which renders nothing", () => {
    renderList(
      [assistantRow("m1", "the answer"), assistantRow("m2", "")],
      undefined,
      vi.fn(),
    );

    expect(screen.getAllByRole("button", { name: "Regenerate" })).toHaveLength(1);
  });

  it("renders no action at all when the surface cannot resend", () => {
    renderList([assistantRow("m1", "the answer")]);

    expect(screen.queryByRole("button", { name: "Regenerate" })).not.toBeInTheDocument();
  });
});
