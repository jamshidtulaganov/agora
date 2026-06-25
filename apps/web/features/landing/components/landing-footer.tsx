"use client";

import Link from "next/link";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { cn } from "@agora/ui/lib/utils";
import { useAuthStore } from "@agora/core/auth";
import { captureDownloadIntent } from "@agora/core/analytics";
import { XMark, githubUrl, twitterUrl } from "./shared";
import { useLocale, locales, localeLabels } from "../i18n";

export function LandingFooter() {
  const { t, locale, setLocale } = useLocale();
  const user = useAuthStore((s) => s.user);
  // New-startup posture: hide GitHub / open-source surfaces, plus links we
  // don't surface yet (changelog, docs — also removed from the top nav).
  const isHidden = (href: string) =>
    href === githubUrl ||
    href === "#open-source" ||
    href === "/changelog" ||
    href.startsWith("/docs");
  const groups = Object.values(t.footer.groups).map((group) => ({
    ...group,
    links: group.links.filter((link) => !isHidden(link.href)),
  }));

  return (
    <footer className="bg-white text-[#18181B] dark:bg-[#0a0d12] dark:text-white">
      <div className="mx-auto max-w-[1320px] px-4 sm:px-6 lg:px-8">
        {/* Top: CTA + link columns */}
        <div className="flex flex-col gap-12 border-b border-zinc-200 py-16 dark:border-white/10 sm:py-20 lg:flex-row lg:gap-20">
          {/* Left — newsletter / CTA */}
          <div className="lg:w-[340px] lg:shrink-0">
            <Link href="#product" className="flex items-center gap-3">
              <AgoraIcon className="size-5 text-foreground" noSpin />
              <span className="text-[18px] font-semibold tracking-[0.04em] lowercase">
                agora
              </span>
            </Link>
            <p className="mt-4 max-w-[300px] text-[14px] leading-[1.7] text-[#71717A] dark:text-white/50 sm:text-[15px]">
              {t.footer.tagline}
            </p>
            <div className="mt-4 flex items-center gap-3">
              <Link
                href={twitterUrl}
                target="_blank"
                rel="noreferrer"
                className="text-[#71717A] transition-colors hover:text-[#18181B] dark:text-white/40 dark:hover:text-white"
              >
                <XMark className="size-4" />
              </Link>
            </div>
            <div className="mt-6">
              <Link
                href={user ? "/" : "/login"}
                className="inline-flex items-center justify-center rounded-[11px] bg-[#2563EB] px-5 py-2.5 text-[13px] font-semibold text-white transition-colors hover:bg-[#2563EB]/88"
              >
                {user ? t.header.dashboard : t.footer.cta}
              </Link>
            </div>
          </div>

          {/* Right — link columns */}
          <div className="grid flex-1 grid-cols-2 gap-8 sm:grid-cols-4">
            {groups.map((group) => (
              <div key={group.label}>
                <h4 className="text-[12px] font-semibold uppercase tracking-[0.1em] text-[#71717A] dark:text-white/40">
                  {group.label}
                </h4>
                <ul className="mt-4 flex flex-col gap-2.5">
                  {group.links.map((link) => (
                    <li key={link.label}>
                      <Link
                        href={link.href}
                        {...(link.href.startsWith("http")
                          ? { target: "_blank", rel: "noreferrer" }
                          : {})}
                        onClick={
                          link.href === "/download"
                            ? () => captureDownloadIntent("landing_footer")
                            : undefined
                        }
                        className="text-[14px] text-[#71717A] transition-colors hover:text-[#18181B] dark:text-white/50 dark:hover:text-white"
                      >
                        {link.label}
                      </Link>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>

        {/* Bottom: copyright + language switcher */}
        <div className="flex items-center justify-between py-6">
          <p className="text-[13px] text-[#A1A1AA] dark:text-white/36">
            {t.footer.copyright.replace(
              "{year}",
              String(new Date().getFullYear()),
            )}
          </p>
          <div className="flex items-center">
            {locales.map((l, i) => (
              <button
                type="button"
                key={l}
                onClick={() => setLocale(l)}
                className={cn(
                  "px-1.5 py-1 text-[12px] font-medium transition-colors",
                  l === locale
                    ? "text-[#3F3F46] dark:text-white/70"
                    : "text-[#A1A1AA] hover:text-[#71717A] dark:text-white/30 dark:hover:text-white/50",
                  i > 0 && "border-l border-zinc-200 dark:border-white/16",
                )}
              >
                {localeLabels[l]}
              </button>
            ))}
          </div>
        </div>

        {/* Giant logo */}
        <div className="relative overflow-hidden pb-4">
          <div className="flex items-end gap-6 sm:gap-8">
            <AgoraIcon
              className="size-[clamp(4rem,12vw,10rem)] shrink-0 text-[#18181B] dark:text-white"
              noSpin
            />
            <span className="font-[family-name:var(--font-serif)] text-[clamp(6rem,22vw,16rem)] font-normal leading-[0.82] tracking-[-0.04em] text-[#18181B] lowercase dark:text-white">
              agora
            </span>
          </div>
        </div>
      </div>
    </footer>
  );
}
