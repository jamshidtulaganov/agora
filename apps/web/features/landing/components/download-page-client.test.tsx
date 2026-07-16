import { describe, expect, it, vi, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createEnDict } from "../i18n/en";
import { DownloadPageClient } from "./download-page-client";
import type { LatestRelease } from "../utils/github-release";

vi.mock("../i18n", () => ({
  useLocale: () => ({
    locale: "en",
    t: createEnDict(true),
    setLocale: () => {},
  }),
}));

// Header/footer pull in auth store + next/link; their behavior is covered
// elsewhere — stub them so this test exercises only the download surface.
vi.mock("./landing-header", () => ({
  LandingHeader: () => <header data-testid="stub-header" />,
}));
vi.mock("./landing-footer", () => ({
  LandingFooter: () => <footer data-testid="stub-footer" />,
}));

const { detectOSMock } = vi.hoisted(() => ({
  detectOSMock: vi.fn(),
}));
vi.mock("../utils/os-detect", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../utils/os-detect")>();
  return { ...actual, detectOS: detectOSMock };
});

const { pageViewedMock, initiatedMock } = vi.hoisted(() => ({
  pageViewedMock: vi.fn(),
  initiatedMock: vi.fn(),
}));
vi.mock("@agora/core/analytics", () => ({
  captureDownloadPageViewed: pageViewedMock,
  captureDownloadInitiated: initiatedMock,
  captureDownloadIntent: vi.fn(),
}));

const dict = createEnDict(true);

function fullRelease(): LatestRelease {
  return {
    version: "v0.3.48",
    publishedAt: "2026-07-16T11:29:56Z",
    htmlUrl: "https://github.com/jamshidtulaganov/agora-cli/releases/tag/v0.3.48",
    assets: {
      macArm64Dmg: "https://dl.test/agora-desktop-0.3.48-mac-arm64.dmg",
      macArm64Zip: "https://dl.test/agora-desktop-0.3.48-mac-arm64.zip",
      winX64Exe: "https://dl.test/agora-desktop-0.3.48-windows-x64.exe",
      winArm64Exe: "https://dl.test/agora-desktop-0.3.48-windows-arm64.exe",
      linuxAmd64AppImage: "https://dl.test/agora-desktop-0.3.48-linux-x86_64.AppImage",
      linuxAmd64Deb: "https://dl.test/agora-desktop-0.3.48-linux-amd64.deb",
      linuxAmd64Rpm: "https://dl.test/agora-desktop-0.3.48-linux-x86_64.rpm",
      linuxArm64AppImage: "https://dl.test/agora-desktop-0.3.48-linux-arm64.AppImage",
      linuxArm64Deb: "https://dl.test/agora-desktop-0.3.48-linux-arm64.deb",
      linuxArm64Rpm: "https://dl.test/agora-desktop-0.3.48-linux-aarch64.rpm",
    },
  };
}

function emptyRelease(): LatestRelease {
  return { version: null, publishedAt: null, htmlUrl: null, assets: {} };
}

beforeEach(() => {
  detectOSMock.mockResolvedValue({ os: "mac", arch: "arm64", archConfident: true });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("DownloadPageClient", () => {
  it("shows the detected-platform hero with the dmg CTA on macOS", async () => {
    render(<DownloadPageClient release={fullRelease()} />);
    await waitFor(() =>
      expect(
        screen.getByRole("link", { name: dict.download.hero.macArm64.primary }),
      ).toHaveAttribute("href", "https://dl.test/agora-desktop-0.3.48-mac-arm64.dmg"),
    );
    expect(screen.getAllByText(dict.download.hero.macArm64.title).length).toBeGreaterThan(0);
  });

  it("lists every platform row with direct asset links", () => {
    render(<DownloadPageClient release={fullRelease()} />);
    expect(screen.getByText(dict.download.allPlatforms.winX64Label)).toBeInTheDocument();
    expect(screen.getByText(dict.download.allPlatforms.winArm64Label)).toBeInTheDocument();
    expect(screen.getByText(dict.download.allPlatforms.linuxX64Label)).toBeInTheDocument();
    expect(screen.getByText(dict.download.allPlatforms.linuxArm64Label)).toBeInTheDocument();
    const appImageLinks = screen.getAllByRole("link", {
      name: dict.download.allPlatforms.formatAppImage,
    });
    expect(appImageLinks).toHaveLength(2);
    expect(appImageLinks[0]).toHaveAttribute(
      "href",
      "https://dl.test/agora-desktop-0.3.48-linux-x86_64.AppImage",
    );
  });

  it("fires page-viewed analytics with the detect payload", async () => {
    render(<DownloadPageClient release={fullRelease()} />);
    await waitFor(() =>
      expect(pageViewedMock).toHaveBeenCalledWith({
        detected_os: "mac",
        detected_arch: "arm64",
        detect_confident: true,
        version_available: true,
      }),
    );
  });

  it("fires download-initiated with matched_detect on the primary CTA", async () => {
    render(<DownloadPageClient release={fullRelease()} />);
    const cta = await screen.findByRole("link", {
      name: dict.download.hero.macArm64.primary,
    });
    fireEvent.click(cta);
    expect(initiatedMock).toHaveBeenCalledWith({
      platform: "mac",
      arch: "arm64",
      format: "dmg",
      primary_cta: true,
      version: "v0.3.48",
      matched_detect: true,
    });
  });

  it("degrades to unavailable state when the release fetch failed", async () => {
    render(<DownloadPageClient release={emptyRelease()} />);
    expect(
      screen.getByText(dict.download.footer.versionUnavailable),
    ).toBeInTheDocument();
    expect(
      screen.getAllByText(dict.download.allPlatforms.unavailable).length,
    ).toBe(5);
    await waitFor(() =>
      expect(pageViewedMock).toHaveBeenCalledWith(
        expect.objectContaining({ version_available: false }),
      ),
    );
  });

  it("shows the version line and release-notes link", () => {
    render(<DownloadPageClient release={fullRelease()} />);
    expect(
      screen.getByText(
        dict.download.footer.currentVersion.replace("{version}", "v0.3.48"),
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("link", {
        name: dict.download.footer.releaseNotes.replace("{version}", "v0.3.48"),
      }),
    ).toHaveAttribute(
      "href",
      "https://github.com/jamshidtulaganov/agora-cli/releases/tag/v0.3.48",
    );
  });
});
