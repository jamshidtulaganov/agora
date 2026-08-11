import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enProjects from "../../locales/en/projects.json";

vi.mock("./project-execution-section", () => ({
  ProjectExecutionSection: () => <div>Workflow controls</div>,
}));

import { ProjectAgentSetupSection } from "./project-agent-setup-section";

function renderSection() {
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { common: enCommon, projects: enProjects } }}
    >
      <ProjectAgentSetupSection projectId="p-1" />
    </I18nProvider>,
  );
}

describe("ProjectAgentSetupSection", () => {
  it("keeps optional project controls behind one accessible entry point", async () => {
    const user = userEvent.setup();
    renderSection();

    const trigger = screen.getByRole("button", { name: "Agent setup" });
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Workflow controls")).not.toBeInTheDocument();

    await user.click(trigger);

    expect(trigger).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByText("Workflow controls")).toBeInTheDocument();
    expect(screen.queryByText("Design controls")).not.toBeInTheDocument();
  });
});
