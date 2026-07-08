// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";

const TEST_RESOURCES = {
  en: { common: enCommon, skills: enSkills },
};

const mockSkills = vi.hoisted(() => ({ value: [] as unknown[] }));

vi.mock("@agora/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({
    skillDetail: (id: string) => `/acme/skills/${id}`,
  }),
}));

vi.mock("@agora/core/workspace/queries", () => ({
  skillListOptions: (wsId: string) => ({
    queryKey: ["workspace", wsId, "skills"],
    queryFn: async () => mockSkills.value,
  }),
}));

vi.mock("../../navigation", () => ({
  AppLink: ({
    href,
    children,
    ...rest
  }: { href: string; children: ReactNode } & Record<string, unknown>) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

import { SkillsNavSidebar } from "./skills-nav-sidebar";

function renderSidebar(currentSkillId: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <SkillsNavSidebar currentSkillId={currentSkillId} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

const SKILLS = [
  { id: "skl-1", name: "Write migration", description: "Generate SQL migration" },
  { id: "skl-2", name: "Review PR", description: "" },
];

describe("SkillsNavSidebar", () => {
  it("renders every skill as a link and marks the current one", async () => {
    mockSkills.value = SKILLS;
    renderSidebar("skl-1");

    const current = await screen.findByRole("link", { current: "page" });
    expect(current).toHaveTextContent("Write migration");
    expect(current).toHaveAttribute("href", "/acme/skills/skl-1");

    const other = screen.getByRole("link", { name: /Review PR/ });
    expect(other).not.toHaveAttribute("aria-current");
    expect(other).toHaveAttribute("href", "/acme/skills/skl-2");
  });

  it("falls back to the no-description label for empty descriptions", async () => {
    mockSkills.value = SKILLS;
    renderSidebar("skl-1");

    await screen.findByRole("link", { current: "page" });
    expect(
      screen.getByText(enSkills.table.no_description),
    ).toBeInTheDocument();
  });

  it("renders nothing while the list is empty", () => {
    mockSkills.value = [];
    const { container } = renderSidebar("skl-1");
    expect(container.querySelector("nav")).toBeNull();
  });
});
