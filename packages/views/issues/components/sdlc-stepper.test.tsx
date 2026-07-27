import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import type { SDLCStage, StagePipeline } from "@agora/core/issues";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";
import { SDLCStepper } from "./sdlc-stepper";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

const BASE_PIPELINE: StagePipeline = {
  current: "review",
  stages: [
    { stage: "dev", state: "passed" },
    { stage: "review", state: "running" },
  ],
};

function renderStepper(
  pipeline: StagePipeline,
  opts: {
    activeLens?: string;
    isLensAvailable?: (stage: SDLCStage) => boolean;
    onSelectStage?: (stage: SDLCStage) => void;
  } = {},
) {
  const onSelectStage = opts.onSelectStage ?? vi.fn();
  const isLensAvailable = opts.isLensAvailable ?? (() => false);
  render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <SDLCStepper
        pipeline={pipeline}
        activeLens={opts.activeLens ?? "issue"}
        isLensAvailable={isLensAvailable}
        onSelectStage={onSelectStage}
      />
    </I18nProvider>,
  );
  return { onSelectStage };
}

describe("SDLCStepper", () => {
  it("renders both stages with their translated labels", () => {
    renderStepper(BASE_PIPELINE);
    expect(screen.getByTestId("sdlc-stage-dev")).toBeInTheDocument();
    expect(screen.getByTestId("sdlc-stage-review")).toBeInTheDocument();
    expect(screen.getByText("Dev")).toBeInTheDocument();
    expect(screen.getByText("Review")).toBeInTheDocument();
    // QA is not a stage — it is an on-demand action reachable at ?lens=qa.
    expect(screen.queryByTestId("sdlc-stage-qa")).toBeNull();
  });

  it("stamps data-state per stage and dims skipped stages", () => {
    // "skipped" is no longer produced by deriveStagePipeline in the 2-stage
    // model, but the StageState stays valid and SDLCStepper is a pure renderer
    // of whatever it's given — covering the visual treatment doesn't depend on
    // derivation reachability.
    const pipeline: StagePipeline = {
      current: "review",
      stages: [
        { stage: "dev", state: "skipped" },
        { stage: "review", state: "running" },
      ],
    };
    renderStepper(pipeline);
    expect(screen.getByTestId("sdlc-stage-dev")).toHaveAttribute("data-state", "skipped");
    expect(screen.getByTestId("sdlc-stage-dev").className).toContain("opacity-40");
    expect(screen.getByTestId("sdlc-stage-review")).toHaveAttribute("data-state", "running");
    expect(screen.getByTestId("sdlc-stage-review").className).not.toContain("opacity-40");
    expect(screen.getByTestId("sdlc-stage-review")).toHaveAttribute("aria-label", "Review: Running");
    expect(screen.getByTestId("sdlc-stage-review")).toHaveAttribute("aria-current", "step");
    expect(screen.getByTestId("sdlc-stage-dev")).not.toHaveAttribute("aria-current");
    expect(screen.getByTestId("sdlc-stage-review").innerHTML).toContain(
      "motion-safe:animate-sdlc-running-ring",
    );
  });

  it("renders an unregistered-lens stage as non-interactive and ignores clicks", () => {
    const { onSelectStage } = renderStepper(BASE_PIPELINE, { isLensAvailable: () => false });
    const devStage = screen.getByTestId("sdlc-stage-dev");
    expect(devStage.tagName).toBe("DIV");
    expect(devStage.className).toContain("cursor-default");
    fireEvent.click(devStage);
    expect(onSelectStage).not.toHaveBeenCalled();
  });

  it("renders a registered-lens stage as interactive and calls onSelectStage on click", () => {
    const { onSelectStage } = renderStepper(BASE_PIPELINE, {
      isLensAvailable: (stage) => stage === "review",
    });
    const reviewStage = screen.getByTestId("sdlc-stage-review");
    expect(reviewStage.tagName).toBe("BUTTON");
    fireEvent.click(reviewStage);
    expect(onSelectStage).toHaveBeenCalledTimes(1);
    expect(onSelectStage).toHaveBeenCalledWith("review");

    // Dev remains unregistered even though Review is — only the registered
    // stage becomes clickable.
    const devStage = screen.getByTestId("sdlc-stage-dev");
    expect(devStage.tagName).toBe("DIV");
    fireEvent.click(devStage);
    expect(onSelectStage).toHaveBeenCalledTimes(1);
  });

  it("marks the stage matching the active lens with the Agora brand tint", () => {
    renderStepper(BASE_PIPELINE, {
      activeLens: "review",
      isLensAvailable: (stage) => stage === "review",
    });
    const review = screen.getByTestId("sdlc-stage-review");
    const dev = screen.getByTestId("sdlc-stage-dev");
    expect(review.className).toContain("bg-brand");
    expect(review.querySelector(".underline")).toBeNull();
    expect(dev.className).not.toContain("bg-brand");
  });

  it("never surfaces the stage detail (the jargon chip was dropped)", () => {
    const pipeline: StagePipeline = {
      current: "review",
      stages: [
        { stage: "dev", state: "passed" },
        { stage: "review", state: "active", detail: "light" },
      ],
    };
    renderStepper(pipeline);
    // The stepper is a clean [dot] [label] beat — "stale"/"light" jargon must
    // not appear anywhere.
    expect(screen.queryByText("stale")).toBeNull();
    expect(screen.queryByText("light")).toBeNull();
  });
});
