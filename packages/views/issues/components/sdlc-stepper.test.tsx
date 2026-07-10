import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import type { SDLCStage, StagePipeline } from "@agora/core/issues";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";
import { SDLCStepper } from "./sdlc-stepper";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

const BASE_PIPELINE: StagePipeline = {
  current: "qa",
  stages: [
    { stage: "design", state: "skipped" },
    { stage: "dev", state: "passed" },
    { stage: "qa", state: "running" },
    { stage: "review", state: "pending" },
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
  it("renders all 4 stages with their translated labels", () => {
    renderStepper(BASE_PIPELINE);
    expect(screen.getByTestId("sdlc-stage-design")).toBeInTheDocument();
    expect(screen.getByTestId("sdlc-stage-dev")).toBeInTheDocument();
    expect(screen.getByTestId("sdlc-stage-qa")).toBeInTheDocument();
    expect(screen.getByTestId("sdlc-stage-review")).toBeInTheDocument();
    expect(screen.getByText("Design")).toBeInTheDocument();
    expect(screen.getByText("Dev")).toBeInTheDocument();
    expect(screen.getByText("QA")).toBeInTheDocument();
    expect(screen.getByText("Review")).toBeInTheDocument();
  });

  it("stamps data-state per stage and dims skipped stages", () => {
    renderStepper(BASE_PIPELINE);
    expect(screen.getByTestId("sdlc-stage-design")).toHaveAttribute("data-state", "skipped");
    expect(screen.getByTestId("sdlc-stage-design").className).toContain("opacity-40");
    expect(screen.getByTestId("sdlc-stage-dev")).toHaveAttribute("data-state", "passed");
    expect(screen.getByTestId("sdlc-stage-dev").className).not.toContain("opacity-40");
    expect(screen.getByTestId("sdlc-stage-qa")).toHaveAttribute("data-state", "running");
    expect(screen.getByTestId("sdlc-stage-review")).toHaveAttribute("data-state", "pending");
  });

  it("renders an unregistered-lens stage as non-interactive and ignores clicks", () => {
    const { onSelectStage } = renderStepper(BASE_PIPELINE, { isLensAvailable: () => false });
    const designStage = screen.getByTestId("sdlc-stage-design");
    expect(designStage.tagName).toBe("DIV");
    expect(designStage.className).toContain("cursor-default");
    fireEvent.click(designStage);
    expect(onSelectStage).not.toHaveBeenCalled();
  });

  it("renders a registered-lens stage as interactive and calls onSelectStage on click", () => {
    const { onSelectStage } = renderStepper(BASE_PIPELINE, {
      isLensAvailable: (stage) => stage === "qa",
    });
    const qaStage = screen.getByTestId("sdlc-stage-qa");
    expect(qaStage.tagName).toBe("BUTTON");
    fireEvent.click(qaStage);
    expect(onSelectStage).toHaveBeenCalledTimes(1);
    expect(onSelectStage).toHaveBeenCalledWith("qa");

    // Dev remains unregistered even though QA is — only the registered
    // stage becomes clickable.
    const devStage = screen.getByTestId("sdlc-stage-dev");
    expect(devStage.tagName).toBe("DIV");
    fireEvent.click(devStage);
    expect(onSelectStage).toHaveBeenCalledTimes(1);
  });

  it("underlines the stage matching the active lens", () => {
    renderStepper(BASE_PIPELINE, { activeLens: "qa", isLensAvailable: (stage) => stage === "qa" });
    expect(screen.getByTestId("sdlc-stage-qa").className).toContain("underline");
    expect(screen.getByTestId("sdlc-stage-dev").className).not.toContain("underline");
  });

  it("renders the detail suffix as a tiny uppercase label when present", () => {
    const pipeline: StagePipeline = {
      current: "review",
      stages: [
        { stage: "design", state: "skipped" },
        { stage: "dev", state: "passed" },
        { stage: "qa", state: "passed", detail: "stale" },
        { stage: "review", state: "active", detail: "light" },
      ],
    };
    renderStepper(pipeline);
    expect(screen.getByText("stale")).toBeInTheDocument();
    expect(screen.getByText("light")).toBeInTheDocument();
  });
});
