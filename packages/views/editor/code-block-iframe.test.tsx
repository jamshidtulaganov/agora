import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { CodeBlockIframe, withNetworkBlockedCSP } from "./code-block-iframe";

describe("withNetworkBlockedCSP", () => {
  it("injects the CSP meta as the first child of an existing head", () => {
    const out = withNetworkBlockedCSP("<html><head><title>x</title></head><body>hi</body></html>");
    expect(out.indexOf("Content-Security-Policy")).toBeLessThan(out.indexOf("<title>"));
    expect(out).toContain("default-src 'none'");
  });

  it("prepends the meta when the document has no head", () => {
    const out = withNetworkBlockedCSP("<div>bare fragment</div>");
    expect(out.startsWith("<meta http-equiv=\"Content-Security-Policy\"")).toBe(true);
  });

  it("keeps inline script and style allowed, everything else denied", () => {
    const out = withNetworkBlockedCSP("<p/>");
    expect(out).toContain("script-src 'unsafe-inline'");
    expect(out).toContain("style-src 'unsafe-inline'");
    expect(out).not.toContain("connect-src http");
  });
});

describe("CodeBlockIframe restrictNetwork", () => {
  it("applies the CSP to srcDoc only when restrictNetwork is set", () => {
    const { container, rerender } = render(
      <CodeBlockIframe html="<p>hi</p>" title="t" restrictNetwork />,
    );
    const framed = container.querySelector("iframe");
    expect(framed?.getAttribute("srcdoc")).toContain("Content-Security-Policy");
    // sandbox invariant untouched
    expect(framed?.getAttribute("sandbox")).toBe("allow-scripts");

    rerender(<CodeBlockIframe html="<p>hi</p>" title="t" />);
    expect(container.querySelector("iframe")?.getAttribute("srcdoc")).not.toContain(
      "Content-Security-Policy",
    );
  });
});
