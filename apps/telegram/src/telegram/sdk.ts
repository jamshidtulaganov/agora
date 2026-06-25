// Minimal typed surface over the Telegram WebApp SDK (loaded via the script tag
// in index.html). We model only what the app consumes; everything is optional
// because the app may also run in a plain browser (preview/dev) where
// window.Telegram is absent.

export interface TelegramThemeParams {
  bg_color?: string;
  text_color?: string;
  hint_color?: string;
  link_color?: string;
  button_color?: string;
  button_text_color?: string;
  secondary_bg_color?: string;
  header_bg_color?: string;
  accent_text_color?: string;
  section_bg_color?: string;
  destructive_text_color?: string;
}

export interface TelegramUser {
  id: number;
  first_name?: string;
  last_name?: string;
  username?: string;
  language_code?: string;
  photo_url?: string;
}

export interface TelegramWebApp {
  initData: string;
  initDataUnsafe: {
    user?: TelegramUser;
    start_param?: string;
  };
  version: string;
  colorScheme: "light" | "dark";
  themeParams: TelegramThemeParams;
  isExpanded: boolean;
  viewportStableHeight: number;
  ready(): void;
  expand(): void;
  onEvent(event: string, handler: () => void): void;
  offEvent(event: string, handler: () => void): void;
  HapticFeedback?: {
    impactOccurred(style: "light" | "medium" | "heavy"): void;
    notificationOccurred(type: "error" | "success" | "warning"): void;
    selectionChanged(): void;
  };
  BackButton?: {
    isVisible: boolean;
    show(): void;
    hide(): void;
    onClick(cb: () => void): void;
    offClick(cb: () => void): void;
  };
}

declare global {
  interface Window {
    Telegram?: { WebApp?: TelegramWebApp };
  }
}

export function getWebApp(): TelegramWebApp | null {
  return window.Telegram?.WebApp ?? null;
}

/** True when running inside an actual Telegram client (initData is present). */
export function isTelegramEnv(): boolean {
  const wa = getWebApp();
  return !!wa && typeof wa.initData === "string" && wa.initData.length > 0;
}

/** The raw signed initData string POSTed to /auth/telegram/miniapp. */
export function getInitData(): string {
  return getWebApp()?.initData ?? "";
}

export function getTelegramUser(): TelegramUser | null {
  return getWebApp()?.initDataUnsafe.user ?? null;
}

/** The deep-link payload (startapp=...) the app was opened with, if any. */
export function getStartParam(): string | null {
  return getWebApp()?.initDataUnsafe.start_param ?? null;
}

/** Telegram UI language (e.g. "ru", "en", "uz"); falls back to browser. */
export function getLanguageCode(): string {
  return (
    getTelegramUser()?.language_code ??
    (typeof navigator !== "undefined" ? navigator.language : "en")
  );
}

/** Tell Telegram the app is ready and request the full-height viewport. */
export function telegramReady(): void {
  const wa = getWebApp();
  if (!wa) return;
  wa.ready();
  wa.expand();
}

export function haptic(style: "light" | "medium" | "heavy" = "light"): void {
  getWebApp()?.HapticFeedback?.impactOccurred(style);
}
