import type { Metadata } from "next";
import { fetchLatestRelease } from "@/features/landing/utils/github-release";
import { DownloadPageClient } from "@/features/landing/components/download-page-client";

export const metadata: Metadata = {
  title: "Download",
  description:
    "Download the Agora desktop app for macOS, Windows, and Linux — or install the CLI for servers and headless setups.",
  openGraph: {
    title: "Download Agora",
    description:
      "Get the Agora desktop app for macOS, Windows, and Linux, with a bundled daemon and zero setup.",
    url: "/download",
  },
  alternates: {
    canonical: "/download",
  },
};

// The release lookup uses fetch with revalidate (see github-release.ts), so
// this page is ISR: at most one GitHub API call per revalidate window.
export default async function DownloadPage() {
  const release = await fetchLatestRelease();
  return <DownloadPageClient release={release} />;
}
