import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { ProgressRing } from "@agora/ui/components/ui/progress-ring";

// The readiness ring is the release cockpit's net-new gestalt (Ship cards +
// health strip). These pin its contract: it renders an accessible role="img"
// with a caller label (or a % fallback), exposes a center content slot, and
// carries the tone color that both surfaces read off the shared readiness
// classifier.

describe("ProgressRing", () => {
  it("renders an accessible ring with the given aria-label and center content", () => {
    render(
      <ProgressRing value={0.8} tone="ready" aria-label="8 of 10 tasks passed">
        8/10
      </ProgressRing>,
    );
    const ring = screen.getByRole("img", { name: "8 of 10 tasks passed" });
    expect(ring).toBeInTheDocument();
    expect(ring.querySelector("svg")).toBeTruthy();
    expect(screen.getByText("8/10")).toBeInTheDocument();
  });

  it("falls back to a percentage aria-label when none is provided", () => {
    render(<ProgressRing value={0.42} />);
    expect(screen.getByRole("img", { name: "42%" })).toBeInTheDocument();
  });

  it("clamps out-of-range / non-finite values without crashing", () => {
    render(<ProgressRing value={Number.NaN} aria-label="nan" />);
    // NaN clamps to 0 → "0%" would be the fallback, but an explicit label wins.
    expect(screen.getByRole("img", { name: "nan" })).toBeInTheDocument();
  });

  it("applies the tone color class per state", () => {
    const { rerender } = render(<ProgressRing value={1} tone="ready" aria-label="r" />);
    expect(screen.getByRole("img", { name: "r" })).toHaveClass("text-emerald-500");

    rerender(<ProgressRing value={0} tone="blocked" aria-label="r" />);
    expect(screen.getByRole("img", { name: "r" })).toHaveClass("text-destructive");

    rerender(<ProgressRing value={0.5} tone="close" aria-label="r" />);
    expect(screen.getByRole("img", { name: "r" })).toHaveClass("text-amber-500");

    rerender(<ProgressRing value={0.1} tone="far" aria-label="r" />);
    expect(screen.getByRole("img", { name: "r" })).toHaveClass("text-muted-foreground");
  });
});
