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
  it("renders all 3 stages with their translated labels", () => {
    renderStepper(BASE_PIPELINE);
    expect(screen.getByTestId("sdlc-stage-dev")).toBeInTheDocument();
    expect(screen.getByTestId("sdlc-stage-qa")).toBeInTheDocument();
    expect(screen.getByTestId("sdlc-stage-review")).toBeInTheDocument();
    expect(screen.getByText("Dev")).toBeInTheDocument();
    expect(screen.getByText("QA")).toBeInTheDocument();
    expect(screen.getByText("Review")).toBeInTheDocument();
  });

  it("stamps data-state per stage and dims skipped stages", () => {
    // "skipped" is no longer produced by deriveStagePipeline in the 3-stage
    // model (design owned the only skip path), but the StageState stays
    // valid and SDLCStepper is a pure renderer of whatever it's given —
    // covering the visual treatment doesn't depend on derivation reachability.
    const pipeline: StagePipeline = {
      current: "qa",
      stages: [
        { stage: "dev", state: "skipped" },
        { stage: "qa", state: "running" },
        { stage: "review", state: "pending" },
      ],
    };
    renderStepper(pipeline);
    expect(screen.getByTestId("sdlc-stage-dev")).toHaveAttribute("data-state", "skipped");
    expect(screen.getByTestId("sdlc-stage-dev").className).toContain("opacity-40");
    expect(screen.getByTestId("sdlc-stage-qa")).toHaveAttribute("data-state", "running");
    expect(screen.getByTestId("sdlc-stage-qa").className).not.toContain("opacity-40");
    expect(screen.getByTestId("sdlc-stage-review")).toHaveAttribute("data-state", "pending");
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

  it("marks the stage matching the active lens as selected (bg fill + underlined label)", () => {
    renderStepper(BASE_PIPELINE, { activeLens: "qa", isLensAvailable: (stage) => stage === "qa" });
    const qa = screen.getByTestId("sdlc-stage-qa");
    const dev = screen.getByTestId("sdlc-stage-dev");
    // Selected: subtle fill on the stage, underline on the LABEL span only
    // (not the whole button — the detail chip must not get underlined).
    expect(qa.className).toContain("bg-accent");
    expect(qa.querySelector(".underline")).not.toBeNull();
    expect(dev.className).not.toContain("bg-accent");
    expect(dev.querySelector(".underline")).toBeNull();
  });

  it("renders the detail suffix as a tiny uppercase label when present", () => {
    const pipeline: StagePipeline = {
      current: "review",
      stages: [
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
