import { useEffect, useRef, useState } from "react";
import { useAuthStore } from "@agora/core/auth";
import { getApi } from "@agora/core/api";
import { getInitData } from "../telegram/sdk";

export type AuthStatus = "loading" | "authed" | "error";

export interface TelegramAuthState {
  status: AuthStatus;
  /** "not-telegram" when opened outside Telegram; "auth-failed" on a 401/network. */
  reason: "not-telegram" | "auth-failed" | null;
  retry: () => void;
}

// Drives Mini App authentication.
//
// The Telegram initData is the source of truth for WHO is using the app, so on
// boot we ALWAYS exchange it for a fresh session — we do NOT trust a persisted
// token. A token left in this webview's localStorage by a previous user (e.g.
// an admin who tested it, or a session from before a bot switch) must never be
// shown to the current Telegram user. So we:
//   1. wait for CoreProvider's AuthInitializer to settle,
//   2. clear any persisted session,
//   3. exchange the current initData via POST /auth/telegram/miniapp,
//   4. only report "authed" once THIS exchange completes.
// A returning user re-auths to the same account, so the only cost is one request
// per open — cheap, and the price of never leaking another user's data.
export function useTelegramAuth(): TelegramAuthState {
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const [done, setDone] = useState(false);
  const [reason, setReason] = useState<TelegramAuthState["reason"]>(null);
  const [nonce, setNonce] = useState(0);
  const startedRef = useRef(false);

  useEffect(() => {
    // Wait for AuthInitializer's boot check to settle, then run exactly once.
    if (isLoading || startedRef.current) return;

    const initData = getInitData();
    if (!initData) {
      // Opened outside Telegram — there is no identity to trust.
      setReason("not-telegram");
      return;
    }

    startedRef.current = true;
    setReason(null);
    void (async () => {
      try {
        // Drop any persisted session BEFORE exchanging, so a stale token can
        // never be shown — not during the request, and not if it fails.
        useAuthStore.getState().logout();
        const { token } = await getApi().telegramMiniAppLogin(initData);
        await useAuthStore.getState().loginWithToken(token);
        setDone(true);
      } catch {
        useAuthStore.getState().logout();
        setReason("auth-failed");
      }
    })();
  }, [isLoading, nonce]);

  // "authed" only once THIS session's exchange finished — a hydrated token alone
  // (done === false) stays "loading" so it can't flash a previous user's data.
  if (done && user) return { status: "authed", reason: null, retry };
  if (reason) return { status: "error", reason, retry };
  return { status: "loading", reason: null, retry };

  function retry() {
    startedRef.current = false;
    setDone(false);
    setReason(null);
    setNonce((n) => n + 1);
  }
}
