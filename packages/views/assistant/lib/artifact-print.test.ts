import { afterEach, describe, expect, it } from "vitest";
import { buildPrintDocument, cloneWithResolvedPaint, printInSandbox } from "./artifact-print";

describe("buildPrintDocument", () => {
  it("wraps the chat's rendered body with a title, print styles and the no-network CSP", () => {
    const doc = buildPrintDocument(
      { kind: "table", title: "Q3 <revenue>", content: "{}" },
      "<table><tr><td>42</td></tr></table>",
    );
    expect(doc).toContain('http-equiv="Content-Security-Policy"');
    expect(doc).toContain("default-src 'none'");
    expect(doc).toContain('<h1 class="artifact-title">Q3 &lt;revenue&gt;</h1>');
    expect(doc).toContain("<td>42</td>");
    expect(doc).toContain("window.print()");
  });

  it("prints an HTML artifact as its own document, ignoring the rendered body", () => {
    const doc = buildPrintDocument(
      { kind: "html", title: "Dashboard", content: "<div id='app'>live</div>" },
      "<p>ignored</p>",
    );
    expect(doc).toContain("<div id='app'>live</div>");
    expect(doc).not.toContain("ignored");
    expect(doc).toContain("Content-Security-Policy");
  });

  it("omits the heading for an untitled artifact", () => {
    const doc = buildPrintDocument({ kind: "markdown", title: "  ", content: "" }, "<p>x</p>");
    expect(doc).not.toContain('<h1 class="artifact-title">');
  });
});

describe("cloneWithResolvedPaint", () => {
  it("copies resolved colours inline and leaves the live tree untouched", () => {
    const root = document.createElement("div");
    root.innerHTML = '<span style="color: rgb(1, 2, 3)">hi</span>';
    document.body.append(root);

    const clone = cloneWithResolvedPaint(root);

    expect((clone.querySelector("span") as HTMLElement).style.color).toBe("rgb(1, 2, 3)");
    expect(clone).not.toBe(root);
    root.remove();
  });
});

describe("printInSandbox", () => {
  afterEach(() => {
    document.querySelectorAll("iframe").forEach((frame) => frame.remove());
  });

  it("uses an opaque-origin frame and replaces an earlier one", () => {
    printInSandbox("<p>one</p>");
    printInSandbox("<p>two</p>");

    const frames = document.querySelectorAll("iframe");
    expect(frames).toHaveLength(1);
    const frame = frames[0]!;
    expect(frame.getAttribute("sandbox")).toBe("allow-scripts allow-modals");
    expect(frame.getAttribute("sandbox")).not.toContain("allow-same-origin");
    expect(frame.srcdoc).toBe("<p>two</p>");
  });
});
