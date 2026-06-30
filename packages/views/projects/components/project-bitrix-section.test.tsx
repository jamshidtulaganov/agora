import React from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";

const { mutateAsync } = vi.hoisted(() => ({ mutateAsync: vi.fn().mockResolvedValue({}) }));
let projectData: Record<string, unknown> | null = null;

vi.mock("@tanstack/react-query", () => ({ useQuery: () => ({ data: projectData }) }));
vi.mock("@agora/core/projects/queries", () => ({
  projectDetailOptions: () => ({ queryKey: ["project", "p1"], queryFn: vi.fn() }),
}));
vi.mock("@agora/core/bitrix", () => ({
  useSyncBitrixProject: () => ({ mutateAsync, isPending: false }),
}));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/ui/components/ui/button", () => ({
  Button: ({
    children,
    onClick,
    disabled,
  }: {
    children: React.ReactNode;
    onClick?: () => void;
    disabled?: boolean;
  }) => (
    <button type="button" onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
}));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { ProjectBitrixSection } from "./project-bitrix-section";

describe("ProjectBitrixSection", () => {
  beforeEach(() => mutateAsync.mockClear());

  it("renders nothing for a non-Bitrix project", () => {
    projectData = { id: "p1", description: "plain project", settings: {} };
    const { container } = render(<ProjectBitrixSection projectId="p1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows last-synced + a Sync button for a Bitrix-linked project and triggers sync", async () => {
    projectData = {
      id: "p1",
      description: "Imported from Bitrix bitrix_group:42",
      settings: { bitrix_synced_at: "2026-06-30T08:00:00Z" },
    };
    render(<ProjectBitrixSection projectId="p1" />);
    expect(screen.getByText(/Last synced/)).toBeTruthy();

    fireEvent.click(screen.getByText("Sync Bitrix"));
    await waitFor(() => expect(mutateAsync).toHaveBeenCalled());
  });

  it("shows the Sync button from the bitrix_group marker even before the first sync", () => {
    projectData = { id: "p1", description: "bitrix_group:7", settings: {} };
    render(<ProjectBitrixSection projectId="p1" />);
    expect(screen.getByText("Sync Bitrix")).toBeTruthy();
    expect(screen.getByText("Not synced yet")).toBeTruthy();
  });
});
