// @vitest-environment jsdom

import type { ReactNode } from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSkills from "../../locales/en/skills.json";
import type { SkillRow } from "./skill-card";

const TEST_RESOURCES = {
  en: { common: enCommon, skills: enSkills },
};

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

import { SkillCard } from "./skill-card";

function makeRow(overrides: Partial<SkillRow> = {}): SkillRow {
  return {
    skill: {
      id: "skl-1",
      workspace_id: "ws-1",
      name: "Write migration",
      description: "Generate and validate SQL migration",
      config: {},
      created_by: "user-1",
      created_at: "2026-07-01T00:00:00Z",
      updated_at: "2026-07-01T00:00:00Z",
    },
    agents: [],
    creator: null,
    runtime: null,
    canEdit: true,
    ...overrides,
  };
}

function renderCard(row: SkillRow) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <SkillCard row={row} href="/acme/skills/skl-1" />
    </I18nProvider>,
  );
}

describe("SkillCard", () => {
  it("renders name, description, and links to the detail page", () => {
    renderCard(makeRow());
    const link = screen.getByRole("link");
    expect(link).toHaveAttribute("href", "/acme/skills/skl-1");
    expect(screen.getByText("Write migration")).toBeInTheDocument();
    expect(
      screen.getByText("Generate and validate SQL migration"),
    ).toBeInTheDocument();
  });

  it("shows the unused label when no agents use the skill", () => {
    renderCard(makeRow());
    expect(screen.getByText(enSkills.table.unused)).toBeInTheDocument();
  });

  it("shows the manual source label and no-description fallback", () => {
    renderCard(
      makeRow({
        skill: { ...makeRow().skill, description: "" },
      }),
    );
    expect(screen.getByText(enSkills.table.source_manual)).toBeInTheDocument();
    expect(
      screen.getByText(enSkills.table.no_description),
    ).toBeInTheDocument();
  });
});
