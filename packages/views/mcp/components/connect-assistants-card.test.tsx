import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { configStore } from "@agora/core/config";

vi.mock("@agora/core/paths", () => ({
  useWorkspacePaths: () => ({ settings: () => "/acme/settings", plugins: () => "/acme/plugins" }),
}));
vi.mock("../../navigation", () => ({
  AppLink: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>{children}</a>
  ),
}));

import { ConnectAssistantsCard } from "./mcp-servers-panel";

describe("ConnectAssistantsCard", () => {
  afterEach(() => configStore.getState().setRuntimeConfig({}));

  it("is hidden when the server reports no connector URL", () => {
    configStore.getState().setRuntimeConfig({ mcpConnectorUrl: "" });
    const { container } = render(<ConnectAssistantsCard />);
    expect(container).toBeEmptyDOMElement();
  });

  it("shows the connector URL and links to token settings for disconnecting", () => {
    configStore.getState().setRuntimeConfig({ mcpConnectorUrl: "https://mcp.example.com/mcp" });
    render(<ConnectAssistantsCard />);
    expect(screen.getByLabelText("Connector URL")).toHaveValue("https://mcp.example.com/mcp");
    expect(screen.getByRole("link", { name: "Settings → Tokens" })).toHaveAttribute(
      "href",
      "/acme/settings?tab=tokens",
    );
  });
});
