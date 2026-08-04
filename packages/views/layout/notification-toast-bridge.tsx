"use client";

import { useEffect, useRef } from "react";
import {
  registerForegroundSystemNotificationHandler,
  type SystemNotificationPayload,
} from "@agora/core/platform";
import { paths } from "@agora/core/paths";
import { toast } from "sonner";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";

type AudioContextConstructor = new () => AudioContext;

let notificationAudioContext: AudioContext | null = null;

function getAudioContext(): AudioContext | null {
  if (typeof window === "undefined") return null;
  const audioWindow = window as typeof window & {
    webkitAudioContext?: AudioContextConstructor;
  };
  const Constructor = window.AudioContext ?? audioWindow.webkitAudioContext;
  if (!Constructor) return null;
  try {
    notificationAudioContext ??= new Constructor();
    return notificationAudioContext;
  } catch {
    return null;
  }
}

/** Play a short two-note chime without shipping a separate audio asset. */
export function playNotificationChime(): void {
  const context = getAudioContext();
  if (!context) return;

  void context.resume().catch(() => {
    // Browser autoplay policy may block sound until the first user gesture.
  });

  try {
    const now = context.currentTime;
    const notes = [
      { frequency: 783.99, delay: 0 },
      { frequency: 1046.5, delay: 0.12 },
    ];
    for (const note of notes) {
      const oscillator = context.createOscillator();
      const gain = context.createGain();
      const start = now + note.delay;
      const end = start + 0.22;

      oscillator.type = "sine";
      oscillator.frequency.setValueAtTime(note.frequency, start);
      gain.gain.setValueAtTime(0.0001, start);
      gain.gain.exponentialRampToValueAtTime(0.075, start + 0.025);
      gain.gain.exponentialRampToValueAtTime(0.0001, end);
      oscillator.connect(gain);
      gain.connect(context.destination);
      oscillator.start(start);
      oscillator.stop(end);
    }
  } catch {
    // Audio is enhancement-only; the toast and unread center remain available.
  }
}

/**
 * Focused-app delivery for both hosts. Background delivery remains native
 * (browser Notification / Electron Notification); when Agora is focused this
 * bridge shows an actionable Sonner toast and plays the shared chime.
 */
export function NotificationToastBridge() {
  const { t } = useT("inbox");
  const { push } = useNavigation();
  const pushRef = useRef(push);
  const recentItemsRef = useRef(new Set<string>());

  useEffect(() => {
    pushRef.current = push;
  }, [push]);

  useEffect(() => {
    registerForegroundSystemNotificationHandler(
      (payload: SystemNotificationPayload) => {
        // Reconnects can replay inbox:new. Keep both the toast and sound quiet
        // for duplicates while still allowing a genuinely new later event.
        if (recentItemsRef.current.has(payload.itemId)) return;
        recentItemsRef.current.add(payload.itemId);
        window.setTimeout(
          () => recentItemsRef.current.delete(payload.itemId),
          60_000,
        );

        playNotificationChime();
        toast(payload.title, {
          id: `inbox-${payload.itemId}`,
          description: payload.body || undefined,
          action: payload.slug
            ? {
                label: t(($) => $.page.title),
                onClick: () => {
                  const inboxPath = `${paths.workspace(payload.slug).inbox()}?issue=${encodeURIComponent(
                    payload.issueKey,
                  )}`;
                  pushRef.current(inboxPath);
                },
              }
            : undefined,
        });
      },
    );
    return () => registerForegroundSystemNotificationHandler(null);
  }, [t]);

  return null;
}
