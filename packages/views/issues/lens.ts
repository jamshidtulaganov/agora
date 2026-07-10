"use client";

// SDLC lens registry + `?lens=` query-param plumbing. Same pattern as
// settings-page.tsx's TAB_QUERY_KEY: whitelist against registered keys,
// `navigation.replace` so switches don't pollute history, preserve other
// query params. See docs/sdlc-stage-cockpit-plan.md section 3 (phase C).
//
// The registry is a plain object, not a mutable Map — adding a stage lens in
// a later phase is a one-line addition to LENS_REGISTRY:
//   qa: { key: "qa", Body: QALensBody },
// No registration call, no import-order footgun (a Map populated by
// side-effecting `registerLens()` calls only works if every lens module
// happens to be imported somewhere before render).

import type { ComponentType } from "react";
import type { SDLCStage } from "@agora/core/issues";
import { useNavigation } from "../navigation";
import { QALensBody } from "../qa/components/qa-lens";
import { DesignLensBody } from "./components/design-lens";
import { DevLensBody } from "./components/dev-lens";
import { ReviewLensBody } from "./components/review-lens";

export const LENS_QUERY_KEY = "lens";
export const DEFAULT_LENS_KEY = "issue" as const;

export type LensKey = "issue" | SDLCStage;

export interface StageLens {
  key: LensKey;
  Body: ComponentType<{ issueId: string }>;
}

// issue-detail.tsx special-cases the "issue" key directly: its content
// column (title/description/sub-issues/activity) reads IssueDetail's own
// local state (timeline, comments, description editor draft, etc.), which
// can't be hoisted into a standalone `{ issueId }` component without a much
// larger refactor that phase C doesn't need. This Body is never mounted —
// the entry exists purely so "issue" is a first-class registry member
// (whitelisted by useLensParam below, and visible to any future caller that
// renders a lens standalone, outside issue-detail).
function IssueLensBody() {
  return null;
}

export const LENS_REGISTRY: Partial<Record<LensKey, StageLens>> = {
  issue: { key: "issue", Body: IssueLensBody },
  qa: { key: "qa", Body: QALensBody },
  design: { key: "design", Body: DesignLensBody },
  dev: { key: "dev", Body: DevLensBody },
  review: { key: "review", Body: ReviewLensBody },
};

export function getLens(key: string): StageLens | undefined {
  return (LENS_REGISTRY as Record<string, StageLens | undefined>)[key];
}

export function isLensRegistered(key: string): boolean {
  return getLens(key) !== undefined;
}

/**
 * Reads/writes the `?lens=` query param on the current path. Unknown or
 * unregistered values fall back to "issue" silently — a stale bookmark, a
 * hand-edited URL, or a lens removed in a later release never breaks the
 * page (enum-drift-safe, per CLAUDE.md's API Response Compatibility rules).
 */
export function useLensParam(): { lens: LensKey; setLens: (key: LensKey) => void } {
  const navigation = useNavigation();
  const fromUrl = navigation.searchParams.get(LENS_QUERY_KEY);
  const lens: LensKey =
    fromUrl && isLensRegistered(fromUrl) ? (fromUrl as LensKey) : DEFAULT_LENS_KEY;

  // replace (not push) so lens switches don't pollute browser history —
  // mirrors settings-page.tsx's handleTabChange. Preserve any other query
  // params the page may carry.
  const setLens = (next: LensKey) => {
    const params = new URLSearchParams(navigation.searchParams);
    params.set(LENS_QUERY_KEY, next);
    navigation.replace(`${navigation.pathname}?${params.toString()}`);
  };

  return { lens, setLens };
}
