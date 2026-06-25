import { Instrument_Serif, Noto_Serif_SC } from "next/font/google";
import { LocaleProvider } from "@/features/landing/i18n";
import { getRequestLocale } from "@/lib/request-locale";

const instrumentSerif = Instrument_Serif({
  subsets: ["latin"],
  weight: "400",
  variable: "--font-serif",
});

const notoSerifSC = Noto_Serif_SC({
  subsets: ["latin"],
  weight: "400",
  variable: "--font-serif-zh",
});

const jsonLd = {
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "Organization",
      name: "Agora",
      url: "https://www.agora.dev",
      sameAs: ["https://github.com/multica-ai/multica"],
    },
    {
      "@type": "SoftwareApplication",
      name: "Agora",
      applicationCategory: "ProjectManagement",
      operatingSystem: "Web",
      description:
        "Open-source project management platform that turns coding agents into real teammates.",
      offers: {
        "@type": "Offer",
        price: "0",
        priceCurrency: "USD",
      },
    },
  ],
};

export default async function LandingLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const initialLocale = await getRequestLocale();

  return (
    <>
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd) }}
      />
      <div className={`${instrumentSerif.variable} ${notoSerifSC.variable} landing-light h-full overflow-x-hidden overflow-y-auto bg-white dark:bg-[#05060b]`}>
        <LocaleProvider initialLocale={initialLocale}>{children}</LocaleProvider>
      </div>
    </>
  );
}
