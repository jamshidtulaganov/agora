import type { Metadata } from "next";
import { AgoraLanding } from "@/features/landing/components/agora-landing";
import { RedirectIfAuthenticated } from "@/features/landing/components/redirect-if-authenticated";

export const metadata: Metadata = {
  title: {
    absolute: "Agora — A Task Board for People and AI Agents",
  },
  description:
    "Agora is a task board for your whole company. AI agents work next to your people: they take tasks, do the work, and show you the result.",
  openGraph: {
    title: "Agora — A Task Board for People and AI Agents",
    description:
    "Agora is a task board for your whole company. AI agents work next to your people: they take tasks, do the work, and show you the result.",
    url: "/",
  },
  alternates: {
    canonical: "/",
  },
};

export default function LandingPage() {
  return (
    <>
      <RedirectIfAuthenticated />
      <AgoraLanding />
    </>
  );
}
