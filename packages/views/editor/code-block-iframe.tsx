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
   * Blocks all NETWORK access from inside the document via an injected
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
 * Injects the network-blocking CSP meta as the FIRST child of <head> (CSP
 * <meta> only applies from the head). Falls back to prepending a <head> when
 * the document has none — browsers parse the leading meta into the implied
 * head in that case.
 */
export function withNetworkBlockedCSP(html: string): string {
  const meta =
    '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; script-src \'unsafe-inline\'; style-src \'unsafe-inline\'; img-src data: blob:; font-src data:; media-src data: blob:">';
  const headMatch = /<head(\s[^>]*)?>/i.exec(html);
  if (headMatch) {
    const insertAt = headMatch.index + headMatch[0].length;
    return html.slice(0, insertAt) + meta + html.slice(insertAt);
  }
  return meta + html;
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
