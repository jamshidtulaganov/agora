import { describe, expect, it, vi, afterEach } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createEnDict } from "../i18n/en";
import { LandingTourModal } from "./landing-tour";

vi.mock("../i18n", () => ({
  useLocale: () => ({
    locale: "en",
    t: createEnDict(true),
    setLocale: () => {},
  }),
}));

const dict = createEnDict(true);

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("LandingTourModal", () => {
  it("renders nothing when closed", () => {
    render(<LandingTourModal open={false} onClose={() => {}} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows the first scene and its caption when opened", () => {
    render(<LandingTourModal open onClose={() => {}} />);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(
      screen.getByText(dict.hero.tour.scenes[0]!.caption),
    ).toBeInTheDocument();
    // Assign scene starts on the issue card in Todo (status chip + properties)
    expect(screen.getByText("AGORA-128")).toBeInTheDocument();
    expect(screen.getAllByText(dict.hero.board.todo).length).toBeGreaterThan(0);
  });

  it("auto-advances to the engines scene like a GIF", () => {
    vi.useFakeTimers();
    render(<LandingTourModal open onClose={() => {}} />);
    // Scene 0 sub-steps dwell 900+1000+900+1100 = 3900ms total
    act(() => {
      vi.advanceTimersByTime(4000);
    });
    expect(
      screen.getByText(dict.hero.tour.scenes[1]!.caption),
    ).toBeInTheDocument();
    // One task fanned out across LLM engines
    expect(screen.getByText("Claude Code")).toBeInTheDocument();
    expect(screen.getByText("Codex")).toBeInTheDocument();
    expect(screen.getByText("Gemini CLI")).toBeInTheDocument();
  });

  it("closes via the close button and Escape", () => {
    const onClose = vi.fn();
    render(<LandingTourModal open onClose={onClose} />);
    fireEvent.click(
      screen.getAllByRole("button", { name: dict.hero.tour.close })[1]!,
    );
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
