import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const { getConsent, approve } = vi.hoisted(() => ({ getConsent: vi.fn(), approve: vi.fn() }));
vi.mock("@agora/core/api", () => ({
  api: { getConnectorConsent: getConsent, approveConnectorConsent: approve },
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { email: string } }) => unknown) => sel({ user: { email: "me@example.com" } }),
}));

import { ConnectorConsentPage } from "./connector-consent-page";

function renderPage(request = "sealed") {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <ConnectorConsentPage request={request} />
    </QueryClientProvider>,
  );
}

describe("ConnectorConsentPage", () => {
  const assign = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(window, "location", { value: { assign }, writable: true });
  });

  it("names the app and sends the browser back after Allow", async () => {
    getConsent.mockResolvedValue({ client_id: "agora-claude", client_name: "Claude", cancel_url: "https://mcp.example/cb?error=access_denied" });
    approve.mockResolvedValue({ redirect_url: "https://mcp.example/cb?reply=x" });
    renderPage();
    expect(await screen.findByText("Claude wants to access your Agora account")).toBeInTheDocument();
    expect(screen.getByText("Signed in as me@example.com")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://mcp.example/cb?reply=x"));
    expect(approve).toHaveBeenCalledWith("sealed");
  });

  it("cancels back to the app", async () => {
    getConsent.mockResolvedValue({ client_id: "agora-claude", client_name: "Claude", cancel_url: "https://mcp.example/cb?error=access_denied" });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(assign).toHaveBeenCalledWith("https://mcp.example/cb?error=access_denied");
    expect(approve).not.toHaveBeenCalled();
  });

  it("never navigates to a non-HTTPS URL", async () => {
    getConsent.mockResolvedValue({ client_id: "x", client_name: "Claude", cancel_url: "javascript:alert(1)" });
    approve.mockResolvedValue({ redirect_url: "http://evil.example" });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));
    await waitFor(() => expect(approve).toHaveBeenCalled());
    expect(assign).not.toHaveBeenCalled();
  });

  it("shows an expired message for a bad or missing request", async () => {
    getConsent.mockRejectedValue(new Error("expired"));
    renderPage();
    expect(await screen.findByText("This connection link expired")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Allow" })).toBeNull();
    renderPage("");
    expect(screen.getAllByText("This connection link expired").length).toBeGreaterThan(0);
  });
});
