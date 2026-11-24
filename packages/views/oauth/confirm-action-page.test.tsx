import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const { getOp, confirmOp, rejectOp } = vi.hoisted(() => ({ getOp: vi.fn(), confirmOp: vi.fn(), rejectOp: vi.fn() }));
vi.mock("@agora/core/api", async (orig) => ({
  ...(await orig<typeof import("@agora/core/api")>()),
  api: { getAssistantOperation: getOp, confirmAssistantOperation: confirmOp, rejectAssistantOperation: rejectOp },
}));

import { ConfirmActionPage } from "./confirm-action-page";

const pending = { id: "op1", tool_name: "delete_issue", summary: "Delete issue HAN-1", workspace_slug: "team", target: { type: "issue", identifier: "HAN-1", title: "Probe" }, status: "pending" };

function renderPage(id = "op1") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ConfirmActionPage operationId={id} />
    </QueryClientProvider>,
  );
}

describe("ConfirmActionPage", () => {
  beforeEach(() => vi.clearAllMocks());

  it("shows what will happen and confirms only on the human click", async () => {
    getOp.mockResolvedValue(pending);
    confirmOp.mockResolvedValue({});
    renderPage();
    expect(await screen.findByText("Delete issue HAN-1")).toBeInTheDocument();
    expect(screen.getByText("team · HAN-1 · Probe")).toBeInTheDocument();
    expect(confirmOp).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(confirmOp).toHaveBeenCalled());
    expect(confirmOp.mock.calls[0]?.[0]).toBe("op1");
  });

  it("cancels", async () => {
    getOp.mockResolvedValue(pending);
    rejectOp.mockResolvedValue({});
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(rejectOp).toHaveBeenCalledWith("op1"));
  });

  it("shows the outcome of a settled request", async () => {
    getOp.mockResolvedValue({ ...pending, status: "confirmed", outcome: "succeeded" });
    renderPage();
    expect(await screen.findByText("Done")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Confirm" })).toBeNull();
  });

  it("handles a missing or foreign request", async () => {
    getOp.mockRejectedValue(new Error("not found"));
    renderPage();
    expect(await screen.findByText("Request not found")).toBeInTheDocument();
  });
});
