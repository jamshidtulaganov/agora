import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const setConfig = vi.hoisted(() => vi.fn());
const resetConfig = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));

vi.mock("@agora/core/projects/queries", () => ({
  projectConfigOptions: () => ({
    queryKey: ["project", "p-1", "config"],
    queryFn: async () => [
      {
        key: "AGORA_TELEGRAM_REPORT_CHAT_ID",
        kind: "string",
        category: "Automation",
        label: "Telegram report chat",
        description: "Project report destination.",
        value: "",
        overridden_by_project: false,
      },
    ],
  }),
}));

vi.mock("@agora/core/projects/mutations", () => ({
  useSetProjectConfig: () => ({ mutate: setConfig, isPending: false }),
  useResetProjectConfig: () => ({ mutate: resetConfig, isPending: false }),
}));

import { ProjectPipelineSection } from "./project-pipeline-section";

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <ProjectPipelineSection projectId="p-1" embedded />
    </QueryClientProvider>,
  );
}

describe("ProjectPipelineSection", () => {
  beforeEach(() => vi.clearAllMocks());

  it("saves project-scoped string settings without treating them as numbers", async () => {
    const user = userEvent.setup();
    renderSection();

    const input = await screen.findByRole("textbox");
    await user.type(input, "-1001234567890");
    await user.tab();

    expect(setConfig).toHaveBeenCalledWith(
      { key: "AGORA_TELEGRAM_REPORT_CHAT_ID", value: "-1001234567890" },
      expect.objectContaining({ onError: expect.any(Function) }),
    );
  });
});
