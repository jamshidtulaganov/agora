"use client";

/**
 * Shared HTML preview iframe.
 *
 * Used by:
 *   - InlineHtmlIframe inside AttachmentCard (HTML attachments inline preview)
 *   - CodeBlockView for fenced ```html blocks (editable Tiptap NodeView)
 *   - HtmlBlockPreview for fenced ```html blocks (ReadonlyContent)
 *   - AttachmentPreviewModal's full-screen HTML kind
 *
 * Sandbox semantics:
 *   sandbox="allow-scripts" (NOT "allow-same-origin")
 *   → iframe runs in an opaque origin: scripts execute (chart JS works),
 *     but cookie / localStorage / parent access / top-nav / popups / forms
 *     remain blocked. This is the standard "preview untrusted HTML" model
 *     (HTML spec §iframe sandbox, MDN, Claude artifacts, v0.dev preview).
 *
 * The server-side `text/plain` + `nosniff` defense at
 * /api/attachments/{id}/content remains untouched — we only feed iframe.srcDoc
 * the text body we fetched, never point iframe.src at the proxy URL.
 */

import { cn } from "@agora/ui/lib/utils";

interface CodeBlockIframeProps {
  /** Document source for srcDoc. Empty string renders a blank frame. */
  html: string;
  /** Iframe title for accessibility. */
  title: string;
  className?: string;
  /** Tailwind height token; defaults to h-[480px]. */
  heightClassName?: string;
  /**
   * Blocks CSP-governed resource requests via an injected
   * CSP <meta> (default-src 'none'; inline script/style allowed). The
   * sandbox attribute isolates the DOM but does NOT stop fetch()/img/beacon
   * to external hosts — for model-generated content (assistant HTML
   * artifacts) that is an exfiltration channel, so the artifact viewer sets
   * this. User-uploaded attachment previews keep today's behavior (their
   * HTML may legitimately load external images).
   */
  restrictNetwork?: boolean;
}

/**
 * Place a trusted head before any supplied HTML. Scanning for a <head> token
 * is unsafe because it may occur inside script/style raw text, comments, or
 * attributes. Parsing the untrusted input before adding CSP is also unsafe:
 * an inert DOMParser document may still fetch resources. The browser parses
 * the supplied HTML as body content after our real head. Its document tags
 * are ignored, while rendered content and inline scripts/styles remain.
 *
 * CSP controls resource requests but does not reliably prohibit navigation
 * from this sandboxed frame; callers must not treat it as total isolation.
 */
const NETWORK_BLOCKED_CSP =
  "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:";

export function withNetworkBlockedCSP(html: string): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${NETWORK_BLOCKED_CSP}">`;
  return `<!doctype html><html><head>${meta}</head><body>${html}</body></html>`;
}

export function CodeBlockIframe({
  html,
  title,
  className,
  heightClassName = "h-[480px]",
  restrictNetwork = false,
}: CodeBlockIframeProps) {
  return (
    <iframe
      // srcDoc keeps the body in the parent's process but isolated to an
      // opaque origin via sandbox. Critical that we never combine
      // `allow-scripts` with `allow-same-origin` — that pairing defeats the
      // sandbox per the HTML spec (notes on the sandbox attribute).
      srcDoc={restrictNetwork ? withNetworkBlockedCSP(html) : html}
      sandbox="allow-scripts"
      title={title}
      className={cn(
        "w-full rounded-md border border-border bg-background",
        heightClassName,
        className,
      )}
    />
  );
}
