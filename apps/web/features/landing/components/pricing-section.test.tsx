import { describe, expect, it, vi, afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { createEnDict } from "../i18n/en";
import { createRuDict } from "../i18n/ru";
import { createUzDict } from "../i18n/uz";
import { createZhDict } from "../i18n/zh";
import { PricingSection } from "./pricing-section";

const auth = vi.hoisted(() => ({ user: null as null | { id: string } }));

vi.mock("@agora/core/auth", () => ({
  useAuthStore: (sel: (s: typeof auth) => unknown) => sel(auth),
}));
vi.mock("@agora/core/config", () => ({
  useConfigStore: (sel: (s: { allowSignup: boolean }) => unknown) =>
    sel({ allowSignup: true }),
}));
vi.mock("../i18n", () => ({
  useLocale: () => ({ locale: "en", t: createEnDict(true), setLocale: () => {} }),
}));

afterEach(() => {
  cleanup();
  auth.user = null;
});

describe("PricingSection", () => {
  it("shows both plans with the agreed prices", () => {
    render(<PricingSection />);
    expect(screen.getByText("$0")).toBeInTheDocument();
    expect(screen.getByText("$12")).toBeInTheDocument();
    expect(screen.getByText("$15 per member if billed monthly")).toBeInTheDocument();
    expect(screen.getByText("14-day free trial")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Contact sales" })).toHaveAttribute(
      "href",
      "/contact-sales",
    );
  });

  it("sends logged-out visitors to signup and logged-in ones to the app", () => {
    render(<PricingSection />);
    expect(screen.getByRole("link", { name: "Start free" })).toHaveAttribute("href", "/signup");
    cleanup();
    auth.user = { id: "u1" };
    render(<PricingSection />);
    expect(screen.getByRole("link", { name: "Start the trial" })).toHaveAttribute("href", "/");
  });

  it("has pricing copy in every language", () => {
    for (const dict of [createRuDict(true), createUzDict(true), createZhDict(true)]) {
      expect(dict.pricing.free.features).toHaveLength(5);
      expect(dict.pricing.team.features).toHaveLength(5);
      expect(dict.pricing.team.price).toBe("$12");
    }
  });
});
