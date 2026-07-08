"use client";

import { useState } from "react";
import Link from "next/link";
import { Check, Globe, Menu, X } from "lucide-react";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { ThemeToggle } from "@agora/ui/components/common/theme-toggle";
import { cn } from "@agora/ui/lib/utils";
import { useAuthStore } from "@agora/core/auth";
import { localeLabels, locales, useLocale, type Locale } from "../i18n";
import { headerButtonClassName } from "./shared";

// Compact code shown on the switcher button; the dropdown uses the full
// localeLabels names.
const localeShortLabels: Record<Locale, string> = {
  en: "EN",
  "zh-Hans": "中文",
  uz: "UZ",
  ru: "RU",
};

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
            {/* Explicit brand color: landing pins tokens to light, so
                text-foreground would render the mark near-black on the dark
                hero and it disappears. */}
            <AgoraIcon className="size-6 text-[#2563EB]" noSpin />
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
          <LanguageSwitcher variant={variant} />
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

function LanguageSwitcher({
  variant,
}: {
  variant: "dark" | "light" | "auto";
}) {
  const { t, locale, setLocale } = useLocale();
  const [open, setOpen] = useState(false);

  return (
    <div className="relative">
      <button
        type="button"
        aria-label={t.header.language}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={cn(
          headerButtonClassName("ghost", variant),
          "gap-1.5 px-2.5 sm:px-3",
        )}
      >
        <Globe className="size-4" aria-hidden />
        <span className="text-[12px] font-semibold tracking-wide">
          {localeShortLabels[locale]}
        </span>
      </button>

      {open ? (
        <>
          {/* click-away backdrop */}
          <button
            type="button"
            tabIndex={-1}
            aria-hidden
            className="fixed inset-0 z-40 cursor-default"
            onClick={() => setOpen(false)}
          />
          <div
            className={cn(
              "absolute right-0 top-[calc(100%+8px)] z-50 w-44 rounded-[12px] border p-1.5 shadow-[0_18px_60px_rgba(0,0,0,0.18)]",
              variant === "auto"
                ? "border-[#0a0d12]/10 bg-white text-[#0a0d12] dark:border-white/14 dark:bg-[#070a10]/95 dark:text-white"
                : variant === "dark"
                  ? "border-white/14 bg-[#070a10]/95 text-white"
                  : "border-[#0a0d12]/10 bg-white text-[#0a0d12]",
            )}
          >
            {locales.map((l) => (
              <button
                type="button"
                key={l}
                onClick={() => {
                  setLocale(l);
                  setOpen(false);
                }}
                className={cn(
                  "flex w-full items-center gap-2 rounded-[9px] px-3 py-2 text-left text-[13px] font-medium transition-colors",
                  variant === "auto"
                    ? "hover:bg-[#0a0d12]/5 dark:hover:bg-white/8"
                    : variant === "dark"
                      ? "hover:bg-white/8"
                      : "hover:bg-[#0a0d12]/5",
                  l !== locale &&
                    (variant === "auto"
                      ? "text-[#0a0d12]/68 dark:text-white/72"
                      : variant === "dark"
                        ? "text-white/72"
                        : "text-[#0a0d12]/68"),
                )}
              >
                {localeLabels[l]}
                {l === locale ? (
                  <Check className="ml-auto size-3.5" aria-hidden />
                ) : null}
              </button>
            ))}
          </div>
        </>
      ) : null}
    </div>
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
