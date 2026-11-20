"use client";

import { cn } from "@agora/ui/lib/utils";
import { LandingHeader } from "./landing-header";
import { LandingHero } from "./landing-hero";
import { WhatSection } from "./what-section";
import { HowSection } from "./how-section";
import { AgentSetupSection, AutomationSection } from "./agent-setup-section";
import { DepartmentsSection } from "./departments-section";
import { ControlSection, InsideSection, ToolsSection } from "./inside-section";
import { PricingSection } from "./pricing-section";
import { FAQSection, FinalCtaSection } from "./faq-section";
import { LandingFooter } from "./landing-footer";
import { ui } from "./shared";

export function AgoraLanding() {
  return (
    <div className={cn("min-h-full text-[16px] leading-[1.55]", ui.page)}>
      <LandingHeader />
      <main>
        <LandingHero />
        <WhatSection />
        <HowSection />
        <AgentSetupSection />
        <AutomationSection />
        <DepartmentsSection />
        <InsideSection />
        <ControlSection />
        <ToolsSection />
        <PricingSection />
        <FAQSection />
        <FinalCtaSection />
      </main>
      <LandingFooter />
    </div>
  );
}
