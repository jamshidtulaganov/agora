"use client";

import { useState, useEffect } from "react";
import { Sparkles, GitBranch, Eye, GitPullRequest, X } from "lucide-react";

// Plain-language "how co-code works" explainer for a developer new to AI agents.
// No jargon — framed around the three things a traditional dev cares about:
// is my code safe (own branch), can I see what changed (review), am I in control
// (accept/reject). Shown automatically the first time, dismissible (persisted
// per-browser), and always re-openable from the "How it works" link.

const DISMISS_KEY = "agora.cocode.howitworks.dismissed";

export function useHowItWorksDismissed() {
  // Default true so the card never flashes for returning users; the effect
  // reveals it for first-timers (nothing stored yet) right after mount.
  const [dismissed, setDismissed] = useState(true);
  useEffect(() => {
    try {
      setDismissed(localStorage.getItem(DISMISS_KEY) === "1");
    } catch {
      setDismissed(false);
    }
  }, []);
  const dismiss = () => {
    try {
      localStorage.setItem(DISMISS_KEY, "1");
    } catch {
      /* ignore */
    }
    setDismissed(true);
  };
  return { dismissed, dismiss };
}

const STEPS = [
  {
    icon: GitBranch,
    title: "1 · Assign",
    body: "Pick an agent — an AI teammate you assign like a person, or @mention one in the chat. It works on its own branch in a copy of the repo, so your main branch is never touched.",
  },
  {
    icon: Eye,
    title: "2 · Review",
    body: "Watch it edit live, or open Source Control (the branch icon) for the full diff. The CI checks tell you whether it builds and the tests pass.",
  },
  {
    icon: GitPullRequest,
    title: "3 · Decide",
    body: "Accept → Open PR turns the work into a normal pull request you review and merge. Discard throws the changes away. Nothing ships without you.",
  },
];

export function EditorHowItWorks({ onClose }: { onClose: () => void }) {
  return (
    <div className="rounded-lg border border-border bg-muted/30 p-3 text-xs">
      <div className="mb-2 flex items-center gap-1.5">
        <Sparkles className="h-3.5 w-3.5 text-primary" />
        <span className="font-medium text-foreground">How co-code works</span>
        <button
          type="button"
          onClick={onClose}
          title="Dismiss"
          className="ml-auto rounded p-0.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <X className="h-3 w-3" />
        </button>
      </div>
      <p className="mb-2.5 leading-snug text-muted-foreground">
        You stay in control — you review and approve everything before it
        merges.
      </p>
      <div className="space-y-2">
        {STEPS.map((s) => {
          const Icon = s.icon;
          return (
            <div key={s.title} className="flex gap-2">
              <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary/80" />
              <div className="min-w-0">
                <div className="font-medium text-foreground">{s.title}</div>
                <div className="leading-snug text-muted-foreground">
                  {s.body}
                </div>
              </div>
            </div>
          );
        })}
      </div>
      <p className="mt-2.5 leading-snug text-muted-foreground">
        Prefer your own tools? Use{" "}
        <span className="font-medium text-foreground">
          Open in your own editor
        </span>{" "}
        to pull the branch into VS Code or your own clone.
      </p>
      <button
        type="button"
        onClick={onClose}
        className="mt-2.5 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90"
      >
        Got it
      </button>
    </div>
  );
}
