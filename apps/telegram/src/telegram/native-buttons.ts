import { useEffect } from "react";
import { getWebApp } from "./sdk";

// Bridge React screens onto Telegram's native chrome buttons. No-ops outside
// Telegram (browser preview), so screens keep their own in-content controls too.

/** Show Telegram's native BackButton while `visible`, wired to `onBack`.
 *  Pass a stable `onBack` (e.g. router.back) to avoid re-binding each render. */
export function useBackButton(visible: boolean, onBack: () => void): void {
  useEffect(() => {
    const bb = getWebApp()?.BackButton;
    if (!bb) return;
    if (!visible) {
      bb.hide();
      return;
    }
    bb.onClick(onBack);
    bb.show();
    return () => {
      bb.offClick(onBack);
      bb.hide();
    };
  }, [visible, onBack]);
}

/** Drive Telegram's native MainButton (the prominent bottom CTA). Click binding
 *  and visibility are split so a changing `onClick` doesn't hide/show the button
 *  (which would flicker on every keystroke). */
export function useMainButton(opts: {
  text: string;
  visible: boolean;
  enabled?: boolean;
  onClick: () => void;
}): void {
  const { text, visible, enabled = true, onClick } = opts;

  // Click handler — rebinds when onClick changes; never touches visibility.
  useEffect(() => {
    const mb = getWebApp()?.MainButton;
    if (!mb) return;
    mb.onClick(onClick);
    return () => mb.offClick(onClick);
  }, [onClick]);

  // Text / enabled / visibility — show() while visible is idempotent.
  useEffect(() => {
    const mb = getWebApp()?.MainButton;
    if (!mb) return;
    mb.setText(text);
    if (enabled) mb.enable();
    else mb.disable();
    if (visible) mb.show();
    else mb.hide();
  }, [text, visible, enabled]);

  // Always hide when the screen unmounts.
  useEffect(() => {
    const mb = getWebApp()?.MainButton;
    return () => mb?.hide();
  }, []);
}
