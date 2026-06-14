import type { Metadata } from "next";
import { AgoraLanding } from "@/features/landing/components/agora-landing";

export const metadata: Metadata = {
  title: "Homepage",
  description:
    "Agora — open-source platform that turns coding agents into real teammates. Assign tasks, track progress, compound skills.",
  openGraph: {
    title: "Agora — Project Management for Human + Agent Teams",
    description:
      "Manage your human + agent workforce in one place.",
    url: "/homepage",
  },
  alternates: {
    canonical: "/homepage",
  },
};

export default function HomepagePage() {
  return <AgoraLanding />;
}
