import { getWebApp } from "./sdk";

// The Mini App renders in Agora's OWN design system — surface, text, border and
// accent colors all come from @agora/ui/styles/tokens.css (imported in
// styles.css), so it reads as Agora rather than a generic Telegram app. We do
// NOT repaint surfaces with the user's Telegram theme; the only thing we take
// from Telegram is the light/dark choice, mapped onto Agora's `.dark` class.

/** Sync Agora's light/dark theme to the Telegram color scheme (or the OS in a
 *  plain-browser preview) and keep it in sync with theme changes. */
export function applyTelegramTheme(): void {
  const wa = getWebApp();

  const prefersDark = () =>
    typeof window !== "undefined" &&
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches;

  const sync = () => {
    const dark = wa ? wa.colorScheme === "dark" : prefersDark();
    document.documentElement.classList.toggle("dark", dark);
  };

  sync();
  wa?.onEvent("themeChanged", sync);
}
