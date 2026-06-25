import type { Metadata } from "next";
import { GuidePageClient } from "@/features/landing/components/guide-page-client";

export const metadata: Metadata = {
  title: "How Agora works — the getting-started guide",
  description:
    "From an empty workspace to a coding agent shipping a pull request — the whole Agora loop, end to end, in seven steps.",
  openGraph: {
    title: "How Agora works — the getting-started guide",
    description:
      "Create a workspace, connect a runtime, assign an issue to an agent, and ship a PR. The complete Agora walkthrough.",
    url: "/guide",
  },
  alternates: {
    canonical: "/guide",
  },
};

export default function GuidePage() {
  return <GuidePageClient />;
}
