import type { Metadata } from "next";
import { TandemLanding } from "@/features/landing/components/tandem-landing";

export const metadata: Metadata = {
  title: "Homepage",
  description:
    "Tandem — open-source platform that turns coding agents into real teammates. Assign tasks, track progress, compound skills.",
  openGraph: {
    title: "Tandem — Project Management for Human + Agent Teams",
    description:
      "Manage your human + agent workforce in one place.",
    url: "/homepage",
  },
  alternates: {
    canonical: "/homepage",
  },
};

export default function HomepagePage() {
  return <TandemLanding />;
}
