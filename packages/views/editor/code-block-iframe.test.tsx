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

  // The regex must not be fooled by a commented-out head: the policy would
  // land inside the comment and silently apply to nothing. Assert at the DOM
  // level, not on strings — document.head must actually contain the meta.
  it("skips a <head> inside an HTML comment and lands in the real head", () => {
    const html =
      "<html><!-- legacy: <head><title>old</title></head> --><head><title>real</title></head><body>x</body></html>";
    const out = withNetworkBlockedCSP(html);
    const doc = new DOMParser().parseFromString(out, "text/html");
    const meta = doc.head.querySelector('meta[http-equiv="Content-Security-Policy"]');
    expect(meta).not.toBeNull();
    // and it must not have been swallowed by the comment
    expect(doc.head.innerHTML).toContain("Content-Security-Policy");
  });

  it("an unclosed comment before head falls back to prepending, which the parser hoists into head", () => {
    const html = "<!-- broken comment <head><body>x</body>";
    const out = withNetworkBlockedCSP(html);
    expect(out.startsWith("<meta http-equiv=\"Content-Security-Policy\"")).toBe(true);
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(
      doc.head.querySelector('meta[http-equiv="Content-Security-Policy"]'),
    ).not.toBeNull();
  });

  it("headless documents get a DOM-effective policy via prepend-hoisting", () => {
    const out = withNetworkBlockedCSP("<div>bare fragment</div>");
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(
      doc.head.querySelector('meta[http-equiv="Content-Security-Policy"]'),
    ).not.toBeNull();
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
