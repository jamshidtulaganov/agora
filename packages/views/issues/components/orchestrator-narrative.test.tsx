import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import type { StagePipeline } from "@agora/core/issues";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";
import { OrchestratorNarrative } from "./orchestrator-narrative";

vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Dev Bot" }),
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <span data-testid="actor-avatar" />,
}));

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

function renderNarrative(pipeline: StagePipeline) {
  render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <OrchestratorNarrative pipeline={pipeline} orchestratorAgentId="agent-dev" />
    </I18nProvider>,
  );
}

describe("OrchestratorNarrative", () => {
  it("names the human approval wait after automated review completes", () => {
    renderNarrative({
      current: "review",
      stages: [
        { stage: "dev", state: "passed" },
        { stage: "review", state: "active", detail: "awaiting approval" },
      ],
    });

    expect(screen.getByText("Dev Bot")).toBeInTheDocument();
    expect(screen.getByText("Awaiting approval")).toBeInTheDocument();
    expect(screen.queryByText("In review")).not.toBeInTheDocument();
  });
});
