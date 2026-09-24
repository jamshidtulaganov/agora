import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import type { AssistantMessage } from "@agora/core/types";
import { NavigationProvider } from "../../navigation";
import type { NavigationAdapter } from "../../navigation";
import { RESOURCES } from "../../locales";
import { MessageList } from "./message-list";

const writeText = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
const getAssistantArtifact = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@agora/core/api")>();
  return { ...actual, api: { ...actual.api, getAssistantArtifact } };
});

function renderList(messages: AssistantMessage[], onRegenerate?: () => void) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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
      <QueryClientProvider client={client}>
        <NavigationProvider value={nav}>
          <MessageList messages={messages} onRegenerate={onRegenerate} />
        </NavigationProvider>
      </QueryClientProvider>
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

function artifact(overrides: Record<string, unknown> = {}) {
  return {
    id: "art-1",
    session_id: "session-1",
    title: "Sprint report",
    kind: "markdown",
    content: "## Done this week\n\nShipped the login fix.",
    version: 1,
    created_at: "2026-09-16T10:00:00Z",
    updated_at: "2026-09-16T10:00:00Z",
    ...overrides,
  };
}

describe("MessageList — inline artifacts", () => {
  beforeEach(() => {
    getAssistantArtifact.mockReset();
  });

  it("shows the artifact itself in the conversation, with Print and Download", async () => {
    getAssistantArtifact.mockResolvedValue(artifact());
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 1 },
      }),
    ]);

    expect(await screen.findByText("Shipped the login fix.")).toBeInTheDocument();
    expect(screen.getByText("Sprint report")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Print or save as PDF" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Download" })).toBeEnabled();
    // v1 carries no badge — the version only earns pixels once it changed.
    expect(screen.queryByText("v1")).not.toBeInTheDocument();
    expect(getAssistantArtifact).toHaveBeenCalledWith("art-1");
  });

  it("shows a revised artifact once, at its latest row, and keeps the earlier row as a note", async () => {
    getAssistantArtifact.mockResolvedValue(artifact({ version: 2, content: "Second draft." }));
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 1 },
      }),
      toolMessage({
        id: "m2",
        tool_name: "update_artifact",
        created_at: "2026-09-16T10:05:00Z",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 2 },
      }),
    ]);

    expect(await screen.findAllByText("Second draft.")).toHaveLength(1);
    expect(screen.getByText("Created Sprint report")).toBeInTheDocument();
    expect(screen.getByText("v2")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Download" })).toHaveLength(1);
  });

  it("says so, with a retry, when the artifact can't be read", async () => {
    getAssistantArtifact.mockRejectedValue(new Error("404"));
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 1 },
      }),
    ]);

    expect(await screen.findByText("This artifact isn't available.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Download" })).toBeDisabled();
    getAssistantArtifact.mockResolvedValue(artifact());
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Shipped the login fix.")).toBeInTheDocument();
  });

  it("prints from a sandboxed frame, never the app window", async () => {
    getAssistantArtifact.mockResolvedValue(artifact());
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 1 },
      }),
    ]);
    await screen.findByText("Shipped the login fix.");

    await userEvent.click(screen.getByRole("button", { name: "Print or save as PDF" }));

    const frame = document.querySelector("iframe[sandbox]") as HTMLIFrameElement | null;
    expect(frame).not.toBeNull();
    expect(frame!.getAttribute("sandbox")).toBe("allow-scripts allow-modals");
    expect(frame!.srcdoc).toContain("Content-Security-Policy");
    expect(frame!.srcdoc).toContain("Sprint report");
    expect(frame!.srcdoc).toContain("Shipped the login fix.");
    expect(frame!.srcdoc).toContain("window.print()");
  });

  it("downloads the file the artifact is best opened as", async () => {
    getAssistantArtifact.mockResolvedValue(artifact());
    const createObjectURL = vi.fn(() => "blob:agora/1");
    const revokeObjectURL = vi.fn();
    Object.assign(URL, { createObjectURL, revokeObjectURL });
    const clicked: string[] = [];
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.download);
    });
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { artifact_id: "art-1", title: "Sprint report", kind: "markdown", version: 1 },
      }),
    ]);
    await screen.findByText("Shipped the login fix.");

    await userEvent.click(screen.getByRole("button", { name: "Download" }));

    expect(clicked).toEqual(["Sprint report.md"]);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    click.mockRestore();
  });

  it("falls back to the plain tool chip when the tool_result shape is unrecognised", () => {
    // Exactly what an older/newer server that renamed the field ships.
    renderList([
      toolMessage({
        id: "m1",
        tool_name: "create_artifact",
        tool_result: { id: "art-1", title: "Sprint report" },
      }),
    ]);

    expect(screen.queryByRole("button", { name: "Download" })).not.toBeInTheDocument();
    expect(getAssistantArtifact).not.toHaveBeenCalled();
    expect(screen.getByText("Created an artifact")).toBeInTheDocument();
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
    renderList([assistantRow("m1", "first"), assistantRow("m2", "second")], onRegenerate);

    const buttons = screen.getAllByRole("button", { name: "Regenerate" });
    expect(buttons).toHaveLength(1);

    await userEvent.click(buttons[0]!);
    expect(onRegenerate).toHaveBeenCalledTimes(1);
  });

  it("skips a trailing pure tool-call turn, which renders nothing", () => {
    renderList([assistantRow("m1", "the answer"), assistantRow("m2", "")], vi.fn());

    expect(screen.getAllByRole("button", { name: "Regenerate" })).toHaveLength(1);
  });

  it("renders no action at all when the surface cannot resend", () => {
    renderList([assistantRow("m1", "the answer")]);

    expect(screen.queryByRole("button", { name: "Regenerate" })).not.toBeInTheDocument();
  });
});
