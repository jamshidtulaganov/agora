import { getWebApp, type TelegramThemeParams } from "./sdk";

// Blend the user's Telegram theme into the app's design tokens. We override the
// SURFACE + TEXT tokens (so the Mini App matches the surrounding Telegram chat)
// but deliberately leave --brand / --primary at the Agora royal blue defined in
// @agora/ui tokens.css, so the accent stays on-brand across every Telegram theme.

function setVar(name: string, value: string | undefined) {
  if (value) document.documentElement.style.setProperty(name, value);
}

function applyParams(params: TelegramThemeParams) {
  const surface = params.secondary_bg_color ?? params.section_bg_color;
  setVar("--background", params.bg_color);
  setVar("--foreground", params.text_color);
  setVar("--card", surface ?? params.bg_color);
  setVar("--card-foreground", params.text_color);
  setVar("--popover", surface ?? params.bg_color);
  setVar("--popover-foreground", params.text_color);
  setVar("--muted", surface ?? params.bg_color);
  setVar("--muted-foreground", params.hint_color);
  setVar("--secondary", surface ?? params.bg_color);
  setVar("--secondary-foreground", params.text_color);
  setVar("--accent", surface ?? params.bg_color);
  setVar("--accent-foreground", params.text_color);
  // Telegram gives no border/ring color — derive a faint line from the text
  // color so dividers read on both light and dark themes.
  if (params.text_color) {
    const faint = `color-mix(in srgb, ${params.text_color} 14%, transparent)`;
    setVar("--border", faint);
    setVar("--input", faint);
  }
  setVar("--destructive", params.destructive_text_color);
}

/** Apply the current Telegram theme and keep it in sync with theme changes. */
export function applyTelegramTheme(): void {
  const wa = getWebApp();
  if (!wa) return;

  const sync = () => {
    document.documentElement.classList.toggle("dark", wa.colorScheme === "dark");
    applyParams(wa.themeParams ?? {});
  };
  sync();
  wa.onEvent("themeChanged", sync);
}
