"use client";

import { useEffect } from "react";
import { useAuthStore } from "@agora/core/auth";
import { api } from "@agora/core/api";

// First-touch marketing attribution for the /admin Marketing view.
//
// While signed out (landing, /login, /signup) the FIRST visit's UTM tags,
// referring site and first page are kept in this browser. Once the person is
// signed in, they are sent to the server one time and removed here. The server
// keeps them only for accounts created in the last 7 days, so an old account
// that later visits the landing page is not counted as a new sign-up.
// Only the referrer's host and the page path are kept — never query strings,
// which can carry personal data.

const KEY = "agora_first_touch";

type FirstTouch = {
  source: string;
  medium: string;
  campaign: string;
  referrer: string;
  landing_path: string;
  first_seen_at: string;
};

function read(): FirstTouch | null {
  try {
    const raw = window.localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as FirstTouch) : null;
  } catch {
    return null;
  }
}

function capture() {
  if (read()) return;
  const params = new URLSearchParams(window.location.search);
  let referrer = "";
  try {
    const ref = document.referrer ? new URL(document.referrer) : null;
    if (ref && ref.host !== window.location.host) referrer = `${ref.protocol}//${ref.host}`;
  } catch {
    referrer = "";
  }
  const touch: FirstTouch = {
    source: params.get("utm_source") ?? params.get("ref") ?? "",
    medium: params.get("utm_medium") ?? "",
    campaign: params.get("utm_campaign") ?? "",
    referrer,
    landing_path: window.location.pathname,
    first_seen_at: new Date().toISOString(),
  };
  try {
    window.localStorage.setItem(KEY, JSON.stringify(touch));
  } catch {
    // Private mode / storage blocked: attribution is best effort.
  }
}

export function FirstTouchTracker() {
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);

  useEffect(() => {
    if (isLoading) return;
    if (!user) {
      capture();
      return;
    }
    const touch = read();
    if (!touch) return;
    api
      .postMyAttribution(touch)
      .then(() => {
        try {
          window.localStorage.removeItem(KEY);
        } catch {
          // ignore
        }
      })
      .catch(() => {
        // Keep it and try again on the next load.
      });
  }, [user, isLoading]);

  return null;
}
