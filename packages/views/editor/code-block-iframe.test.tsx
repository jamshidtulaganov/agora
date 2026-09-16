import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { CodeBlockIframe, withNetworkBlockedCSP } from "./code-block-iframe";

function cspMetaIn(html: string) {
  const doc = new DOMParser().parseFromString(html, "text/html");
  return doc.head.querySelectorAll('meta[http-equiv="Content-Security-Policy"]');
}

describe("withNetworkBlockedCSP", () => {
  it("injects exactly one CSP meta as the first child of an existing head", () => {
    const out = withNetworkBlockedCSP("<html><head><title>x</title></head><body>hi</body></html>");
    const metas = cspMetaIn(out);
    expect(metas).toHaveLength(1);
    expect(metas[0]?.getAttribute("content")).toContain("default-src 'none'");
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(doc.head.firstElementChild?.getAttribute("http-equiv")).toBe("Content-Security-Policy");
  });

  // A "<head>" string inside script raw text captured the old textual
  // insertion and left the parsed document with no effective policy.
  it("is not fooled by \"<head>\" inside script content", () => {
    const out = withNetworkBlockedCSP(
      '<html><head><title>t</title></head><body><script type="application/json">"<head>"</script></body></html>',
    );
    expect(cspMetaIn(out)).toHaveLength(1);
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(doc.querySelector("script")?.textContent).toBe('"<head>"');
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

  it("survives an unclosed comment before head", () => {
    const out = withNetworkBlockedCSP("<!-- broken comment <head><body>x</body>");
    expect(cspMetaIn(out)).toHaveLength(1);
  });

  it("keeps a real policy before head-like text in style and attributes", () => {
    const out = withNetworkBlockedCSP(
      '<style>.label::after { content: "<head>"; }</style><div data-template="<head>">safe</div>',
    );
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(cspMetaIn(out)).toHaveLength(1);
    expect(doc.head.firstElementChild?.getAttribute("http-equiv")).toBe("Content-Security-Policy");
    expect(doc.querySelector("style")?.textContent).toContain('"<head>"');
    expect(doc.querySelector("div")?.getAttribute("data-template")).toBe("<head>");
  });

  it("keeps the policy in a malformed document with stray closing tags", () => {
    const out = withNetworkBlockedCSP(
      '</head></body></html><script type="application/json">"<head>"</script><p>still visible',
    );
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(cspMetaIn(out)).toHaveLength(1);
    expect(doc.head.firstElementChild?.getAttribute("http-equiv")).toBe("Content-Security-Policy");
    expect(doc.body.textContent).toContain("still visible");
  });

  it("preserves the doctype and standards mode when the input has one", () => {
    const out = withNetworkBlockedCSP("<!doctype html><html><head></head><body>x</body></html>");
    const doc = new DOMParser().parseFromString(out, "text/html");
    expect(doc.compatMode).toBe("CSS1Compat");
    expect(cspMetaIn(out)).toHaveLength(1);
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
    const content = cspMetaIn(out)[0]?.getAttribute("content") ?? "";
    expect(content).toContain("script-src 'unsafe-inline'");
    expect(content).toContain("style-src 'unsafe-inline'");
    expect(content).toContain("default-src 'none'");
    expect(content).not.toContain("connect-src http");
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
