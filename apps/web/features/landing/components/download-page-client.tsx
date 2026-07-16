"use client";

import { useEffect, useState } from "react";
import { Apple, Check, Copy, Download, Monitor, Terminal } from "lucide-react";
import {
  captureDownloadInitiated,
  captureDownloadPageViewed,
  type DownloadInitiatedPayload,
} from "@agora/core/analytics";
import { LandingHeader } from "./landing-header";
import { LandingFooter } from "./landing-footer";
import { Reveal } from "./reveal";
import { useLocale } from "../i18n";
import { detectOS, type DetectResult } from "../utils/os-detect";
import type { LatestRelease } from "../utils/github-release";
import { hasAnyAsset } from "../utils/parse-release-assets";

const ACCENT = "#2563EB";

const CLI_INSTALL_CMD =
  "curl -fsSL https://raw.githubusercontent.com/jamshidtulaganov/agora-cli/main/install.sh | bash";
const CLI_START_CMD = "agora daemon start";

interface DownloadPageClientProps {
  release: LatestRelease;
}

function useDetect(): DetectResult | null {
  const [result, setResult] = useState<DetectResult | null>(null);
  useEffect(() => {
    let cancelled = false;
    void detectOS().then((r) => {
      if (!cancelled) setResult(r);
    });
    return () => {
      cancelled = true;
    };
  }, []);
  return result;
}

export function DownloadPageClient({ release }: DownloadPageClientProps) {
  const { t } = useLocale();
  const detect = useDetect();
  const assets = release.assets;
  const versionAvailable = hasAnyAsset(assets);

  useEffect(() => {
    if (!detect) return;
    captureDownloadPageViewed({
      detected_os: detect.os,
      detected_arch: detect.arch,
      detect_confident: detect.archConfident,
      version_available: versionAvailable,
    });
  }, [detect, versionAvailable]);

  const version = release.version ?? "";

  const track = (
    payload: Omit<DownloadInitiatedPayload, "version" | "matched_detect">,
  ) => {
    captureDownloadInitiated({
      ...payload,
      version,
      matched_detect:
        detect?.os === payload.platform && detect?.arch === payload.arch,
    });
  };

  return (
    <>
      <LandingHeader />
      <main className="bg-white text-[#18181B] dark:bg-[#05070b] dark:text-white">
        <div className="mx-auto max-w-[1320px] px-4 py-16 sm:px-6 sm:py-20 lg:px-8 lg:py-24">
          {/* Detected-platform hero */}
          <DownloadHero
            detect={detect}
            release={release}
            versionAvailable={versionAvailable}
            track={track}
          />

          {/* All platforms */}
          <Reveal delay={120}>
            <section id="all-platforms" className="mt-20 scroll-mt-24">
              <h2 className="text-[11px] font-semibold uppercase tracking-[0.16em] text-[#71717A] dark:text-white/40">
                {t.download.allPlatforms.title}
              </h2>
              <div className="mt-6 overflow-hidden rounded-2xl border border-zinc-200 dark:border-white/10">
                <PlatformRow
                  icon={<Apple className="size-4" aria-hidden />}
                  label={t.download.allPlatforms.macLabel}
                  note={t.download.allPlatforms.intelNote}
                  links={[
                    {
                      label: t.download.allPlatforms.formatDmg,
                      href: assets.macArm64Dmg,
                      onClick: () =>
                        track({ platform: "mac", arch: "arm64", format: "dmg", primary_cta: false }),
                    },
                    {
                      label: t.download.allPlatforms.formatZip,
                      href: assets.macArm64Zip,
                      onClick: () =>
                        track({ platform: "mac", arch: "arm64", format: "zip", primary_cta: false }),
                    },
                  ]}
                  unavailableLabel={t.download.allPlatforms.unavailable}
                />
                <PlatformRow
                  icon={<Monitor className="size-4" aria-hidden />}
                  label={t.download.allPlatforms.winX64Label}
                  links={[
                    {
                      label: t.download.allPlatforms.formatExe,
                      href: assets.winX64Exe,
                      onClick: () =>
                        track({ platform: "windows", arch: "x64", format: "exe", primary_cta: false }),
                    },
                  ]}
                  unavailableLabel={t.download.allPlatforms.unavailable}
                />
                <PlatformRow
                  icon={<Monitor className="size-4" aria-hidden />}
                  label={t.download.allPlatforms.winArm64Label}
                  links={[
                    {
                      label: t.download.allPlatforms.formatExe,
                      href: assets.winArm64Exe,
                      onClick: () =>
                        track({ platform: "windows", arch: "arm64", format: "exe", primary_cta: false }),
                    },
                  ]}
                  unavailableLabel={t.download.allPlatforms.unavailable}
                />
                <PlatformRow
                  icon={<Terminal className="size-4" aria-hidden />}
                  label={t.download.allPlatforms.linuxX64Label}
                  links={[
                    {
                      label: t.download.allPlatforms.formatAppImage,
                      href: assets.linuxAmd64AppImage,
                      onClick: () =>
                        track({ platform: "linux", arch: "x64", format: "appimage", primary_cta: false }),
                    },
                    {
                      label: t.download.allPlatforms.formatDeb,
                      href: assets.linuxAmd64Deb,
                      onClick: () =>
                        track({ platform: "linux", arch: "x64", format: "deb", primary_cta: false }),
                    },
                    {
                      label: t.download.allPlatforms.formatRpm,
                      href: assets.linuxAmd64Rpm,
                      onClick: () =>
                        track({ platform: "linux", arch: "x64", format: "rpm", primary_cta: false }),
                    },
                  ]}
                  unavailableLabel={t.download.allPlatforms.unavailable}
                />
                <PlatformRow
                  icon={<Terminal className="size-4" aria-hidden />}
                  label={t.download.allPlatforms.linuxArm64Label}
                  links={[
                    {
                      label: t.download.allPlatforms.formatAppImage,
                      href: assets.linuxArm64AppImage,
                      onClick: () =>
                        track({ platform: "linux", arch: "arm64", format: "appimage", primary_cta: false }),
                    },
                    {
                      label: t.download.allPlatforms.formatDeb,
                      href: assets.linuxArm64Deb,
                      onClick: () =>
                        track({ platform: "linux", arch: "arm64", format: "deb", primary_cta: false }),
                    },
                    {
                      label: t.download.allPlatforms.formatRpm,
                      href: assets.linuxArm64Rpm,
                      onClick: () =>
                        track({ platform: "linux", arch: "arm64", format: "rpm", primary_cta: false }),
                    },
                  ]}
                  unavailableLabel={t.download.allPlatforms.unavailable}
                />
              </div>
            </section>
          </Reveal>

          {/* CLI */}
          <Reveal delay={160}>
            <section className="mt-20">
              <h2 className="text-[11px] font-semibold uppercase tracking-[0.16em] text-[#71717A] dark:text-white/40">
                {t.download.cli.title}
              </h2>
              <p className="mt-3 max-w-[640px] text-[14px] leading-[1.7] text-[#71717A] dark:text-white/50 sm:text-[15px]">
                {t.download.cli.sub}
              </p>
              <div className="mt-6 flex max-w-[760px] flex-col gap-3">
                <CommandBlock
                  label={t.download.cli.installLabel}
                  command={CLI_INSTALL_CMD}
                  copyLabel={t.download.cli.copyLabel}
                  copiedLabel={t.download.cli.copiedLabel}
                />
                <CommandBlock
                  label={t.download.cli.startLabel}
                  command={CLI_START_CMD}
                  copyLabel={t.download.cli.copyLabel}
                  copiedLabel={t.download.cli.copiedLabel}
                />
              </div>
              <p className="mt-4 text-[13px] text-[#71717A] dark:text-white/40">
                {t.download.cli.sshNote}
              </p>
            </section>
          </Reveal>

          {/* Version footer */}
          <Reveal delay={200}>
            <div className="mt-20 flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-zinc-200 pt-8 text-[13px] text-[#71717A] dark:border-white/10 dark:text-white/40">
              {release.version ? (
                <>
                  <span>
                    {t.download.footer.currentVersion.replace("{version}", release.version)}
                  </span>
                  {release.htmlUrl ? (
                    <a
                      href={release.htmlUrl}
                      target="_blank"
                      rel="noreferrer"
                      className="transition-colors hover:text-[#18181B] dark:hover:text-white"
                    >
                      {t.download.footer.releaseNotes.replace("{version}", release.version)}
                    </a>
                  ) : null}
                </>
              ) : (
                <span>{t.download.footer.versionUnavailable}</span>
              )}
            </div>
          </Reveal>
        </div>
      </main>
      <LandingFooter />
    </>
  );
}

function DownloadHero({
  detect,
  release,
  versionAvailable,
  track,
}: {
  detect: DetectResult | null;
  release: LatestRelease;
  versionAvailable: boolean;
  track: (
    payload: Omit<DownloadInitiatedPayload, "version" | "matched_detect">,
  ) => void;
}) {
  const { t } = useLocale();
  const assets = release.assets;
  const h = t.download.hero;

  let title = h.unknown.title;
  let sub = h.unknown.sub;
  let primary: { label: string; href: string; onClick: () => void } | null = null;
  let secondary: { label: string; href: string; onClick: () => void } | null = null;
  let hint: string | null = null;

  if (detect?.os === "mac" && versionAvailable) {
    if (assets.macArm64Dmg) {
      title = h.macArm64.title;
      sub = h.macArm64.sub;
      primary = {
        label: h.macArm64.primary,
        href: assets.macArm64Dmg,
        onClick: () =>
          track({ platform: "mac", arch: "arm64", format: "dmg", primary_cta: true }),
      };
      if (assets.macArm64Zip) {
        secondary = {
          label: h.macArm64.altZip,
          href: assets.macArm64Zip,
          onClick: () =>
            track({ platform: "mac", arch: "arm64", format: "zip", primary_cta: true }),
        };
      }
      if (!detect.archConfident) hint = h.safariMacHint;
    } else {
      title = h.macIntel.title;
      sub = h.macIntel.sub;
      hint = h.macIntel.intelHint;
    }
  } else if (detect?.os === "windows" && versionAvailable) {
    const arm = detect.arch === "arm64";
    const href = arm ? assets.winArm64Exe : assets.winX64Exe;
    const variant = arm ? h.winArm64 : h.winX64;
    title = variant.title;
    sub = variant.sub;
    if (href) {
      primary = {
        label: variant.primary,
        href,
        onClick: () =>
          track({
            platform: "windows",
            arch: arm ? "arm64" : "x64",
            format: "exe",
            primary_cta: true,
          }),
      };
    }
    if (!detect.archConfident) hint = h.archFallbackHint;
  } else if (detect?.os === "linux" && versionAvailable) {
    const arm = detect.arch === "arm64";
    const href = arm ? assets.linuxArm64AppImage : assets.linuxAmd64AppImage;
    title = h.linux.title;
    sub = h.linux.sub;
    if (href) {
      primary = {
        label: h.linux.primary,
        href,
        onClick: () =>
          track({
            platform: "linux",
            arch: arm ? "arm64" : "x64",
            format: "appimage",
            primary_cta: true,
          }),
      };
      secondary = { label: h.linux.altFormats, href: "#all-platforms", onClick: () => {} };
    }
    if (!detect.archConfident) hint = h.archFallbackHint;
  }

  return (
    <Reveal>
      <div className="max-w-[840px]">
        <h1 className="font-[family-name:var(--font-serif)] text-[2.6rem] leading-[1.05] tracking-[-0.03em] sm:text-[3.4rem] lg:text-[4.2rem]">
          {title}
        </h1>
        <p className="mt-5 text-[15px] leading-[1.7] text-[#71717A] dark:text-white/50 sm:text-[16px]">
          {sub}
        </p>
        {primary ? (
          <div className="mt-8 flex flex-wrap items-center gap-4">
            <a
              href={primary.href}
              onClick={primary.onClick}
              className="group inline-flex items-center justify-center gap-2 rounded-[12px] px-5 py-3 text-[14px] font-semibold text-white shadow-[0_14px_40px_-12px_rgba(37,99,235,0.7)] transition-transform hover:-translate-y-0.5"
              style={{ backgroundColor: ACCENT }}
            >
              <Download className="size-4" aria-hidden />
              {primary.label}
            </a>
            {secondary ? (
              <a
                href={secondary.href}
                onClick={secondary.onClick}
                className="text-[14px] font-medium text-[#71717A] underline-offset-4 transition-colors hover:text-[#18181B] hover:underline dark:text-white/50 dark:hover:text-white"
              >
                {secondary.label}
              </a>
            ) : null}
          </div>
        ) : null}
        {hint ? (
          <p className="mt-4 text-[13px] text-[#71717A] dark:text-white/40">{hint}</p>
        ) : null}
      </div>
    </Reveal>
  );
}

interface PlatformLink {
  label: string;
  href: string | undefined;
  onClick: () => void;
}

function PlatformRow({
  icon,
  label,
  note,
  links,
  unavailableLabel,
}: {
  icon: React.ReactNode;
  label: string;
  note?: string;
  links: PlatformLink[];
  unavailableLabel: string;
}) {
  const available = links.filter(
    (l): l is PlatformLink & { href: string } => typeof l.href === "string",
  );
  return (
    <div className="flex flex-col gap-2 border-b border-zinc-200 bg-white px-5 py-4 last:border-b-0 dark:border-white/10 dark:bg-transparent sm:flex-row sm:items-center sm:gap-4">
      <div className="flex min-w-[220px] items-center gap-2.5 text-[14px] font-medium">
        <span className="text-[#71717A] dark:text-white/40">{icon}</span>
        {label}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {available.length > 0 ? (
          available.map((link) => (
            <a
              key={link.label}
              href={link.href}
              onClick={link.onClick}
              className="rounded-[9px] border border-zinc-200 px-3 py-1.5 text-[13px] font-medium text-[#3F3F46] transition-colors hover:border-zinc-300 hover:bg-zinc-50 dark:border-white/14 dark:text-white/70 dark:hover:bg-white/[0.06]"
            >
              {link.label}
            </a>
          ))
        ) : (
          <span className="text-[13px] text-[#71717A] dark:text-white/35">
            {unavailableLabel}
          </span>
        )}
      </div>
      {note ? (
        <p className="text-[12px] text-[#71717A] dark:text-white/35 sm:ml-auto">{note}</p>
      ) : null}
    </div>
  );
}

function CommandBlock({
  label,
  command,
  copyLabel,
  copiedLabel,
}: {
  label: string;
  command: string;
  copyLabel: string;
  copiedLabel: string;
}) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1800);
    } catch {
      // Clipboard API unavailable (permissions / insecure context) — the
      // command is selectable text, so manual copy still works.
    }
  };

  return (
    <div className="overflow-hidden rounded-[12px] border border-zinc-200 dark:border-white/10">
      <div className="flex items-center justify-between border-b border-zinc-200 bg-zinc-50 px-4 py-2 dark:border-white/10 dark:bg-white/[0.04]">
        <span className="text-[12px] font-semibold uppercase tracking-[0.1em] text-[#71717A] dark:text-white/40">
          {label}
        </span>
        <button
          type="button"
          onClick={() => void copy()}
          className="inline-flex items-center gap-1.5 text-[12px] font-medium text-[#71717A] transition-colors hover:text-[#18181B] dark:text-white/50 dark:hover:text-white"
        >
          {copied ? (
            <Check className="size-3.5" aria-hidden />
          ) : (
            <Copy className="size-3.5" aria-hidden />
          )}
          {copied ? copiedLabel : copyLabel}
        </button>
      </div>
      <pre className="overflow-x-auto bg-white px-4 py-3 text-[13px] leading-relaxed text-[#18181B] dark:bg-transparent dark:text-white/85">
        <code>{command}</code>
      </pre>
    </div>
  );
}
