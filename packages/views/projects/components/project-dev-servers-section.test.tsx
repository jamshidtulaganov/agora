import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enProjects from "../../locales/en/projects.json";

const TEST_RESOURCES = { en: { common: enCommon, projects: enProjects } };

const mocks = vi.hoisted(() => ({
  setMine: vi.fn(async (_url: string) => undefined),
  deleteMine: vi.fn(async () => undefined),
  servers: [
    { user_id: "u-me", base_url: "https://jamshid.sdteam.uz", updated_at: "" },
    { user_id: "u-mate", base_url: "https://shahzod.sdteam.uz", updated_at: "" },
  ],
}));

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@agora/core/auth", () => ({
  useAuthStore: (selector: (s: { user: { id: string } }) => unknown) =>
    selector({ user: { id: "u-me" } }),
}));

vi.mock("@agora/core/projects", () => ({
  projectDevServersOptions: () => ({
    queryKey: ["ws-1", "p-1", "dev-servers"],
    queryFn: async () => mocks.servers,
  }),
  useSetMyProjectDevServer: () => ({ mutateAsync: mocks.setMine }),
  useDeleteMyProjectDevServer: () => ({ mutateAsync: mocks.deleteMine }),
}));

vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["ws-1", "members"],
    queryFn: async () => [
      { user_id: "u-me", name: "Jamshid", email: "j@x" },
      { user_id: "u-mate", name: "Shahzod", email: "s@x" },
    ],
  }),
}));

import { ProjectDevServersSection } from "./project-dev-servers-section";

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ProjectDevServersSection projectId="p-1" />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

async function openSection(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /dev servers/i }));
}

describe("ProjectDevServersSection", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows my box editable and teammates' boxes read-only", async () => {
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    const input = await screen.findByRole("textbox");
    expect(input).toHaveValue("https://jamshid.sdteam.uz");

    expect(await screen.findByText("Shahzod")).toBeInTheDocument();
    const mateLink = screen.getByRole("link", { name: "https://shahzod.sdteam.uz" });
    expect(mateLink).toHaveAttribute("href", "https://shahzod.sdteam.uz");
  });

  it("saves my URL on blur", async () => {
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    const input = await screen.findByRole("textbox");
    await user.clear(input);
    await user.type(input, "https://jamshid2.sdteam.uz");
    await user.tab();

    expect(mocks.setMine).toHaveBeenCalledWith("https://jamshid2.sdteam.uz");
  });

  it("clearing my URL deletes my box instead of saving an empty one", async () => {
    const user = userEvent.setup();
    renderSection();
    await openSection(user);

    const input = await screen.findByRole("textbox");
    await user.clear(input);
    await user.tab();

    expect(mocks.deleteMine).toHaveBeenCalled();
    expect(mocks.setMine).not.toHaveBeenCalled();
  });
});
