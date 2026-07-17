import type { BaseLayoutProps } from "fumadocs-ui/layouts/shared";
import { ArrowUpRight } from "lucide-react";

// Docs-local stateless Agora mark — the "assembly" ring (matches @agora/ui's
// AgoraIcon), stateless so it's safe to render from Server Components such as
// layout.config.tsx / layout.tsx. Nodes inherit currentColor; the center keeps
// the brand ultramarine. Keep in sync with
// packages/ui/components/common/agora-icon.tsx and /brand if the mark changes.
function AgoraMark() {
  return (
    <svg
      viewBox="0 0 96 96"
      className="inline-block size-[1em]"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="48" cy="48" r="28" fill="none" stroke="currentColor" strokeWidth="1.4" opacity={0.28} />
      <circle cx="48" cy="20" r="8" fill="currentColor" />
      <circle cx="72.2" cy="62" r="8" fill="currentColor" />
      <circle cx="23.8" cy="62" r="8" fill="currentColor" />
      <circle cx="72.2" cy="34" r="6.5" stroke="currentColor" strokeWidth="3" />
      <circle cx="48" cy="76" r="6.5" stroke="currentColor" strokeWidth="3" />
      <circle cx="23.8" cy="34" r="6.5" stroke="currentColor" strokeWidth="3" />
      <circle cx="48" cy="48" r="10" fill="#2347E8" />
    </svg>
  );
}

// External links shown at the top of the sidebar (and in the top nav on
// desktop). Leading icon = brand identity (Agora asterisk); trailing
// ArrowUpRight = "opens externally" glyph, same pattern as
// `packages/views/layout/help-launcher.tsx` from PR #1560.
const externalLinkText = (label: string) => (
  <span className="inline-flex items-center gap-1">
    {label}
    <ArrowUpRight className="size-3 translate-y-px text-muted-foreground/60" />
  </span>
);

export const baseOptions: BaseLayoutProps = {
  nav: {
    title: (
      <span className="font-semibold text-base">Agora Docs</span>
    ),
  },
  links: [
    {
      icon: <AgoraMark />,
      text: externalLinkText("Agora"),
      url: "https://agora.dev",
      external: true,
    },
  ],
};
