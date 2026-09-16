// Stick-to-bottom arithmetic for the transcript, kept pure so the behavior
// can be reasoned about (and tested) without a scroll container.
//
// The rule: follow new content ONLY while the reader is already at the
// bottom. Someone who scrolled up to re-read an earlier answer must never be
// yanked back down mid-sentence — they get the "jump to latest" pill instead.

/** How far from the bottom still counts as "at the bottom", in px. */
export const STICK_TO_BOTTOM_THRESHOLD = 80;

export interface ScrollMetrics {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}

/** Pixels of content left below the viewport. Never negative (elastic
 *  over-scroll on macOS/iOS reports a scrollTop past the end). */
export function distanceFromBottom(metrics: ScrollMetrics): number {
  const distance = metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight;
  return Number.isFinite(distance) ? Math.max(0, distance) : 0;
}

export function isNearBottom(
  metrics: ScrollMetrics,
  threshold: number = STICK_TO_BOTTOM_THRESHOLD,
): boolean {
  return distanceFromBottom(metrics) <= threshold;
}
