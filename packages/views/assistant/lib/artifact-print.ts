// Print (and "Save as PDF") for an assistant artifact shown in the chat.
//
// The print happens in a throwaway sandboxed frame, never in the app window:
// `sandbox="allow-scripts allow-modals"` without allow-same-origin keeps the
// frame on an opaque origin, and the network-blocked CSP from
// CodeBlockIframe keeps it from phoning home — the same security posture the
// on-screen HTML artifact already has (plan §7.1). The frame prints itself
// from a small inline script, because a parent cannot call print() across the
// opaque origin.
//
// For chart / table / document artifacts the frame receives the markup the
// chat already rendered, with each element's resolved colours copied inline:
// charts paint with CSS variables (var(--chart-1)) that do not exist in a
// separate document, so a plain copy would print as an empty frame.

import { withNetworkBlockedCSP } from "../../editor/code-block-iframe";
import { toArtifactKind } from "./artifact";

const PRINT_SCRIPT =
  "<script>window.addEventListener('load',function(){setTimeout(function(){window.print()},60)});</script>";

const PRINT_CSS = `
  @page { margin: 16mm; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; color: #111; font-size: 12pt; line-height: 1.5; margin: 0; }
  h1.artifact-title { font-size: 16pt; margin: 0 0 12pt; }
  h1, h2, h3, h4 { line-height: 1.25; margin: 14pt 0 6pt; }
  p, ul, ol, pre, table { margin: 0 0 8pt; }
  table { border-collapse: collapse; width: 100%; font-size: 10pt; }
  th, td { border: 1px solid #ccc; padding: 4pt 6pt; text-align: left; vertical-align: top; }
  thead th { background: #f3f3f3; }
  td.num, th.num { text-align: right; font-variant-numeric: tabular-nums; }
  pre, code { font-family: ui-monospace, Menlo, monospace; font-size: 9.5pt; white-space: pre-wrap; }
  svg { max-width: 100%; height: auto; }
  tr, img, svg { break-inside: avoid; }
`;

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

export interface PrintableArtifact {
  kind: string;
  title: string;
  content: string;
}

/**
 * The full HTML document the print frame loads. `bodyHtml` is the chat's own
 * rendered markup (see serializeRenderedBody); an HTML artifact ignores it and
 * prints its own document, exactly as it runs on screen.
 */
export function buildPrintDocument(artifact: PrintableArtifact, bodyHtml: string): string {
  if (toArtifactKind(artifact.kind) === "html") {
    return withNetworkBlockedCSP(artifact.content + PRINT_SCRIPT);
  }
  const title = escapeHtml(artifact.title.trim());
  return withNetworkBlockedCSP(
    `<title>${title}</title><style>${PRINT_CSS}</style>` +
      (title ? `<h1 class="artifact-title">${title}</h1>` : "") +
      bodyHtml +
      PRINT_SCRIPT,
  );
}

const SVG_PAINT_PROPS = [
  "fill",
  "stroke",
  "stroke-width",
  "stroke-dasharray",
  "opacity",
  "fill-opacity",
  "stroke-opacity",
  "font-size",
  "font-family",
  "font-weight",
  "text-anchor",
] as const;

const HTML_PAINT_PROPS = ["color", "background-color", "text-align", "font-weight"] as const;

/**
 * Clone `live` with every element's computed paint copied onto the clone's
 * inline style, so the markup renders the same outside this document's
 * stylesheets. Walks the live and cloned trees in lockstep — same structure,
 * so the Nth element of one is the Nth of the other.
 */
export function cloneWithResolvedPaint<T extends Element>(live: T): T {
  const clone = live.cloneNode(true) as T;
  const liveNodes = [live, ...Array.from(live.querySelectorAll("*"))];
  const cloneNodes = [clone, ...Array.from(clone.querySelectorAll("*"))];
  const view = live.ownerDocument.defaultView;
  if (!view) return clone;
  liveNodes.forEach((node, index) => {
    const target = cloneNodes[index] as HTMLElement | SVGElement | undefined;
    if (!target || !("style" in target)) return;
    const computed = view.getComputedStyle(node);
    const props = node instanceof view.SVGElement ? SVG_PAINT_PROPS : HTML_PAINT_PROPS;
    for (const prop of props) {
      const value = computed.getPropertyValue(prop);
      if (value && !value.includes("var(")) target.style.setProperty(prop, value);
    }
  });
  return clone;
}

/** The chat's rendered artifact body as standalone markup for the print frame. */
export function serializeRenderedBody(root: HTMLElement): string {
  return cloneWithResolvedPaint(root).innerHTML;
}

/**
 * The rendered chart as a standalone SVG file, or null when the body holds no
 * chart. Colours are resolved first for the same reason as printing.
 */
export function renderedChartSvg(root: HTMLElement): string | null {
  const svg = root.querySelector("svg.recharts-surface");
  if (!svg) return null;
  const clone = cloneWithResolvedPaint(svg);
  clone.setAttribute("xmlns", "http://www.w3.org/2000/svg");
  const { width, height } = svg.getBoundingClientRect();
  if (width && height) {
    clone.setAttribute("width", String(Math.round(width)));
    clone.setAttribute("height", String(Math.round(height)));
  }
  return new XMLSerializer().serializeToString(clone);
}

let activeFrame: HTMLIFrameElement | null = null;

/**
 * Load `html` into a hidden sandboxed frame that prints itself. One frame at a
 * time: a second print replaces the first. The frame is dropped a while later
 * rather than on `afterprint`, which does not cross the opaque origin.
 */
export function printInSandbox(html: string): void {
  activeFrame?.remove();
  const frame = document.createElement("iframe");
  frame.setAttribute("sandbox", "allow-scripts allow-modals");
  frame.setAttribute("aria-hidden", "true");
  frame.tabIndex = -1;
  frame.style.cssText = "position:fixed;right:0;bottom:0;width:0;height:0;border:0;opacity:0;pointer-events:none;";
  frame.srcdoc = html;
  document.body.append(frame);
  activeFrame = frame;
  window.setTimeout(() => {
    if (activeFrame === frame) activeFrame = null;
    frame.remove();
  }, 5 * 60 * 1000);
}
