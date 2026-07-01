"use client";

import { useState } from "react";
import Link from "next/link";
import { Menu, X } from "lucide-react";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { ThemeToggle } from "@agora/ui/components/common/theme-toggle";
import { cn } from "@agora/ui/lib/utils";
import { useAuthStore } from "@agora/core/auth";
import { useLocale } from "../i18n";
import { headerButtonClassName } from "./shared";

export function LandingHeader({
  variant = "auto",
}: {
  variant?: "dark" | "light" | "auto";
}) {
  const { t } = useLocale();
  const user = useAuthStore((s) => s.user);
  const [isMenuOpen, setIsMenuOpen] = useState(false);
  const navLinks = [
    { href: "/guide", label: t.header.guide },
    { href: "/usecases", label: t.header.useCases },
  ];
  const ctaHref = user ? "/" : "/login";
  const ctaLabel = user ? t.header.dashboard : t.header.cta;

  return (
    <header
      className={cn(
        "relative inset-x-0 top-0 z-30",
        variant === "light"
          ? "border-b border-[#0a0d12]/8 bg-white"
          : "absolute bg-transparent",
      )}
    >
      <div className="mx-auto flex h-[76px] max-w-[1320px] items-center justify-between px-4 sm:px-6 lg:px-8">
        <div className="flex min-w-0 items-center gap-6 lg:gap-8">
          <Link href="/" className="flex shrink-0 items-center gap-3">
            <AgoraIcon className="size-5 text-foreground" noSpin />
            <span
              className={cn(
                "text-[18px] font-semibold tracking-[0.04em] lowercase sm:text-[20px]",
                variant === "auto"
                  ? "text-[#0a0d12] dark:text-white/92"
                  : variant === "dark"
                    ? "text-white/92"
                    : "text-[#0a0d12]",
              )}
            >
              agora
            </span>
          </Link>

          <nav
            aria-label={t.header.navigation}
            className="hidden items-center gap-1 md:flex"
          >
            {navLinks.map((link) => (
              <Link
                key={link.href}
                href={link.href}
                className={navLinkClassName(variant)}
              >
                {link.label}
              </Link>
            ))}
          </nav>
        </div>

        <div className="flex shrink-0 items-center gap-2 sm:gap-2.5">
          {/* Hardcoded classes (not tokens): the landing tree pins tokens to
              light via `.landing-light`, so a token-styled control would stay
              light even in dark mode. These mirror the header's auto variant. */}
          <ThemeToggle
            className={cn(
              "border-transparent bg-transparent",
              variant === "auto"
                ? "text-[#0a0d12]/70 hover:bg-[#0a0d12]/5 hover:text-[#0a0d12] dark:text-white/80 dark:hover:bg-white/8 dark:hover:text-white"
                : variant === "dark"
                  ? "text-white/80 hover:bg-white/8 hover:text-white"
                  : "text-[#0a0d12]/70 hover:bg-[#0a0d12]/5 hover:text-[#0a0d12]",
            )}
          />
          <button
            type="button"
            aria-label={isMenuOpen ? t.header.closeMenu : t.header.openMenu}
            aria-expanded={isMenuOpen}
            onClick={() => setIsMenuOpen((open) => !open)}
            className={cn(
              headerButtonClassName("ghost", variant),
              "px-3 md:hidden",
            )}
          >
            {isMenuOpen ? (
              <X className="size-4" aria-hidden />
            ) : (
              <Menu className="size-4" aria-hidden />
            )}
          </button>
          <Link
            href={ctaHref}
            className={headerButtonClassName("solid", variant)}
          >
            {ctaLabel}
          </Link>
        </div>
      </div>

      {isMenuOpen ? (
        <div
          className={cn(
            "absolute left-4 right-4 top-[calc(100%+8px)] z-50 rounded-[14px] border p-2 shadow-[0_18px_60px_rgba(0,0,0,0.18)] md:hidden",
            variant === "auto"
              ? "border-[#0a0d12]/10 bg-white text-[#0a0d12] dark:border-white/14 dark:bg-[#070a10]/95 dark:text-white"
              : variant === "dark"
                ? "border-white/14 bg-[#070a10]/95 text-white"
                : "border-[#0a0d12]/10 bg-white text-[#0a0d12]",
          )}
        >
          <nav aria-label={t.header.navigation} className="flex flex-col">
            {navLinks.map((link) => (
              <Link
                key={link.href}
                href={link.href}
                onClick={() => setIsMenuOpen(false)}
                className={mobileNavLinkClassName(variant)}
              >
                {link.label}
              </Link>
            ))}
          </nav>
        </div>
      ) : null}
    </header>
  );
}

function navLinkClassName(variant: "dark" | "light" | "auto") {
  return cn(
    "inline-flex h-9 items-center rounded-[9px] px-3 text-[13px] font-medium transition-colors",
    variant === "auto"
      ? "text-[#0a0d12]/62 hover:bg-[#0a0d12]/5 hover:text-[#0a0d12] dark:text-white/72 dark:hover:bg-white/8 dark:hover:text-white"
      : variant === "dark"
        ? "text-white/72 hover:bg-white/8 hover:text-white"
        : "text-[#0a0d12]/62 hover:bg-[#0a0d12]/5 hover:text-[#0a0d12]",
  );
}

function mobileNavLinkClassName(variant: "dark" | "light" | "auto") {
  return cn(
    "flex min-h-11 items-center gap-2 rounded-[10px] px-3 text-[14px] font-medium transition-colors",
    variant === "auto"
      ? "text-[#0a0d12]/68 hover:bg-[#0a0d12]/5 hover:text-[#0a0d12] dark:text-white/76 dark:hover:bg-white/8 dark:hover:text-white"
      : variant === "dark"
        ? "text-white/76 hover:bg-white/8 hover:text-white"
        : "text-[#0a0d12]/68 hover:bg-[#0a0d12]/5 hover:text-[#0a0d12]",
  );
}
