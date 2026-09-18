import { describe, it, expect, afterEach, vi } from "vitest";
import { useState } from "react";
import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import { RESOURCES } from "../../locales";
import { Composer } from "./composer";

/** Mirrors how the real call sites drive the composer: value lives above it. */
function ComposerHarness({
  onSend,
  variant,
}: {
  onSend: (content: string) => void;
  variant?: "docked" | "hero";
}) {
  const [value, setValue] = useState("");
  return (
    <I18nProvider locale="en" resources={RESOURCES}>
      <Composer value={value} onValueChange={setValue} onSend={onSend} variant={variant} />
    </I18nProvider>
  );
}

afterEach(() => {
  cleanup();
});

async function openMenu() {
  const user = userEvent.setup();
  const textarea = screen.getByRole("textbox");
  await user.click(textarea);
  await user.keyboard("/");
  return { user, textarea: textarea as HTMLTextAreaElement };
}

describe("Composer slash menu — opening", () => {
  it('opens on "/" typed as the first character of an empty composer', async () => {
    render(<ComposerHarness onSend={vi.fn()} />);
    await openMenu();

    expect(screen.getByRole("listbox")).toBeInTheDocument();
    expect(screen.getAllByRole("option")).toHaveLength(13);
  });

  it('does NOT open for a "/" typed mid-sentence', async () => {
    render(<ComposerHarness onSend={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("textbox"));
    await user.keyboard("see docs/");

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("works in the hero variant too", async () => {
    render(<ComposerHarness onSend={vi.fn()} variant="hero" />);
    await openMenu();

    expect(screen.getByRole("listbox")).toBeInTheDocument();
  });
});

describe("Composer slash menu — filtering and dismissal", () => {
  it("narrows as the user keeps typing", async () => {
    render(<ComposerHarness onSend={vi.fn()} />);
    const { user } = await openMenu();

    await user.keyboard("us");

    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent("/usage");
  });

  it("closes itself once nothing matches (e.g. a path)", async () => {
    render(<ComposerHarness onSend={vi.fn()} />);
    const { user } = await openMenu();

    await user.keyboard("tmp/local");

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("closes once the user starts a real multi-line message", async () => {
    render(<ComposerHarness onSend={vi.fn()} />);
    const { user } = await openMenu();

    await user.keyboard("{Shift>}{Enter}{/Shift}");

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("closes on Escape and leaves the typed text alone", async () => {
    render(<ComposerHarness onSend={vi.fn()} />);
    const { user, textarea } = await openMenu();

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(textarea).toHaveValue("/");
  });
});

describe("Composer slash menu — picking", () => {
  it("replaces the '/' with the template and leaves the caret at the end", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user, textarea } = await openMenu();

    // First option is /add-task; Enter picks the highlighted one.
    await user.keyboard("{Enter}");

    expect(textarea).toHaveValue("Create a task: ");
    expect(textarea.selectionStart).toBe("Create a task: ".length);
    expect(onSend).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("arrow keys move the highlight before Enter picks", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user, textarea } = await openMenu();

    // /add-task -> /my-tasks -> /find
    await user.keyboard("{ArrowDown}{ArrowDown}{Enter}");

    expect(textarea).toHaveValue("Find issues about ");
    expect(onSend).not.toHaveBeenCalled();
  });

  it("sends immediately for a send-command and keeps its text until success", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user, textarea } = await openMenu();

    await user.keyboard("usage{Enter}");

    expect(onSend).toHaveBeenCalledWith("How much have I used this week?");
    expect(textarea).toHaveValue("How much have I used this week?");
  });

  it("sends the launcher's own prompt for a report recipe", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user } = await openMenu();

    await user.keyboard("sprint{Enter}");

    // Same string the empty-state row sends — one wording per request.
    expect(onSend).toHaveBeenCalledWith(
      "How is the current sprint going? Give me a sprint report.",
    );
  });

  it("keeps the two s-triggers apart", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user } = await openMenu();

    // "/standup" and "/sprint-report" share a first letter; neither is a
    // prefix of the other, so the typed word resolves to exactly one.
    await user.keyboard("stand");
    expect(screen.getAllByRole("option")).toHaveLength(1);

    await user.keyboard("{Enter}");

    expect(onSend).toHaveBeenCalledWith("What did everyone do since yesterday?");
  });

  it("sends a management recipe's own prompt, which has no launcher row", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user } = await openMenu();

    // "/plan-sprint" and "/project" share a first letter; the full word
    // resolves to exactly one.
    await user.keyboard("plan");
    expect(screen.getAllByRole("option")).toHaveLength(1);

    await user.keyboard("{Enter}");

    expect(onSend).toHaveBeenCalledWith(
      "Plan the next sprint from the backlog and propose it as a plan.",
    );
  });

  it("picks with the mouse without stealing focus from the textarea", async () => {
    const onSend = vi.fn();
    render(<ComposerHarness onSend={onSend} />);
    const { user, textarea } = await openMenu();

    await user.click(screen.getByRole("option", { name: /\/digest/ }));

    expect(onSend).toHaveBeenCalledWith("What happened across my workspaces today?");
    expect(textarea).toHaveValue("What happened across my workspaces today?");
  });
});
