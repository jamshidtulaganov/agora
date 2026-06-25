"use client";

import { LandingHeader } from "./landing-header";
import { LandingHero } from "./landing-hero";
import { ProductDemo } from "./product-demo";
import { FeaturesSection } from "./features-section";
import { HowItWorksSection } from "./how-it-works-section";
import { FAQSection } from "./faq-section";
import { LandingFooter } from "./landing-footer";

export function AgoraLanding() {
  return (
    <>
      <div className="relative">
        <LandingHeader />
        <LandingHero />
      </div>

      <ProductDemo />
      <FeaturesSection />
      <HowItWorksSection />
      <FAQSection />
      <LandingFooter />
    </>
  );
}
