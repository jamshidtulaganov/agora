"use client";

import Link from "next/link";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { cn } from "@agora/ui/lib/utils";
import { captureDownloadIntent } from "@agora/core/analytics";
import { docsHrefForLocale, useLocale } from "../i18n";
import { ui } from "./shared";

export function LandingFooter() {
  const { t, locale } = useLocale();
  // New-startup posture: no GitHub / open-source, social, about or changelog
  // links on the public site.
  const links = [
    { href: "/guide", label: t.header.guide },
    { href: "/usecases", label: t.header.useCases },
    { href: "/#pricing", label: t.header.pricing },
    { href: docsHrefForLocale(locale), label: t.header.docs },
    { href: "/download", label: t.header.download },
    { href: "/contact-sales", label: t.header.contactSales },
  ];

  return (
    <footer className={cn("border-t px-4 py-10 sm:px-6", ui.page, ui.line)}>
      <div className="mx-auto flex max-w-[1160px] flex-wrap items-center gap-x-8 gap-y-3">
        <Link href="/" className="flex min-h-11 flex-1 items-center gap-2.5">
          <AgoraIcon className="size-6 text-[#2563EB]" noSpin />
          <span className="text-[17px] font-semibold lowercase tracking-[-0.01em]">agora</span>
          <span className={cn("text-[14px]", ui.muted)}>
            {t.footer.copyright.replace("{year}", String(new Date().getFullYear()))}
          </span>
        </Link>
        <nav aria-label={t.footer.navigation} className="flex flex-wrap gap-x-6 gap-y-1">
          {links.map((link) => (
            <Link
              key={link.href}
              href={link.href}
              onClick={
                link.href === "/download"
                  ? () => captureDownloadIntent("landing_footer")
                  : undefined
              }
              className={cn(
                "py-2.5 text-[15px] transition-colors hover:text-[#0A0D12] dark:hover:text-white",
                ui.muted,
              )}
            >
              {link.label}
            </Link>
          ))}
        </nav>
      </div>
    </footer>
  );
}
