"use client";

import { useCallback, useRef, useState } from "react";
import type { ReactNode } from "react";
import { ArrowLeft, ArrowRight, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { completeOnboarding, useProductTourStore } from "@agora/core/onboarding";
import type { Workspace } from "@agora/core/types";
import { AgoraIcon } from "@agora/ui/components/common/agora-icon";
import { Button } from "@agora/ui/components/ui/button";
import { cn } from "@agora/ui/lib/utils";
import { DragStrip } from "@agora/views/platform";
import { useT } from "../../i18n";
import { StepAbout, StepNotifications, StepProfile, StepWorkspaces } from "./member-setup-steps";

const STEPS = ["about", "profile", "notifications", "workspaces"] as const;
type MemberSetupStep = (typeof STEPS)[number];

/**
 * First-login setup for someone who arrives already belonging to workspaces
 * an importer made for them (the Zoho migration). They do not need to create
 * a workspace or connect a runtime — they need to know what Agora is, put a
 * face and a name on their work, choose how they hear about it, and land in
 * the right workspace. Four short steps, then an in-app tour
 * (ProductTour) over the sidebar.
 *
 * Mounted by OnboardingFlow in place of the create-a-workspace flow, so web
 * and desktop share it through the same onboarding route / overlay.
 */
export function MemberSetupFlow({
  workspaces,
  onComplete,
}: {
  workspaces: Workspace[];
  onComplete: (workspace?: Workspace) => void;
}) {
  const { t } = useT("onboarding");
  const [step, setStep] = useState<MemberSetupStep>("about");
  const [destination, setDestination] = useState<Workspace | null>(workspaces[0] ?? null);
  const [withTour, setWithTour] = useState(true);
  // The current step's "save before moving on" (only the profile has one).
  const saveRef = useRef<(() => Promise<boolean>) | null>(null);
  const registerSave = useCallback((save: (() => Promise<boolean>) | null) => {
    saveRef.current = save;
  }, []);
  const [busy, setBusy] = useState(false);
  const startTour = useProductTourStore((s) => s.start);

  const index = STEPS.indexOf(step);
  const isLast = index === STEPS.length - 1;

  const finish = async () => {
    const target = destination ?? workspaces[0];
    if (!target) return;
    try {
      await completeOnboarding("member_setup", target.id);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t(($) => $.member_setup.finish_failed));
      return;
    }
    if (withTour) startTour(target.id);
    onComplete(target);
  };

  const next = async () => {
    if (busy) return;
    setBusy(true);
    try {
      // A step may need to save before moving on (the profile's name); it
      // reports false to stay put, e.g. when the save failed.
      if (saveRef.current && !(await saveRef.current())) return;
      if (isLast) await finish();
      else setStep(STEPS[index + 1]!);
    } finally {
      setBusy(false);
    }
  };

  const back = () => {
    if (index > 0) setStep(STEPS[index - 1]!);
  };

  const cta = isLast
    ? t(($) => $.member_setup.workspaces.open, { workspace: destination?.name ?? "" })
    : step === "about"
      ? t(($) => $.member_setup.about.cta)
      : t(($) => $.member_setup.continue);

  let body: ReactNode;
  switch (step) {
    case "about":
      body = <StepAbout workspaces={workspaces} />;
      break;
    case "profile":
      body = <StepProfile registerSave={registerSave} />;
      break;
    case "notifications":
      body = <StepNotifications workspaceSlug={workspaces[0]?.slug ?? null} />;
      break;
    case "workspaces":
      body = (
        <StepWorkspaces
          workspaces={workspaces}
          selectedId={destination?.id ?? null}
          onSelect={setDestination}
          withTour={withTour}
          onWithTourChange={setWithTour}
        />
      );
      break;
  }

  return (
    <div className="flex h-full min-h-[640px] flex-col">
      <DragStrip />
      <div className="flex flex-1 flex-col items-center overflow-y-auto px-6 pb-10 sm:px-10">
        <div className="flex w-full max-w-[560px] flex-1 flex-col">
          <header className="flex items-center justify-between pb-10 pt-2">
            <div className="flex items-center gap-2">
              <AgoraIcon className="size-5 text-brand" />
              <span className="font-serif text-lg font-medium tracking-tight">
                {t(($) => $.welcome.wordmark)}
              </span>
            </div>
            <ol className="flex items-center gap-1.5" aria-label={t(($) => $.member_setup.progress, { step: index + 1, total: STEPS.length })}>
              {STEPS.map((name, i) => (
                <li
                  key={name}
                  aria-current={i === index ? "step" : undefined}
                  className={cn(
                    "h-1.5 rounded-full transition-all",
                    i === index ? "w-6 bg-brand" : i < index ? "w-1.5 bg-brand/50" : "w-1.5 bg-muted-foreground/25",
                  )}
                />
              ))}
            </ol>
          </header>

          <div key={step} className="animate-onboarding-enter flex-1">
            {body}
          </div>

          <footer className="mt-10 flex items-center justify-between gap-3">
            {index > 0 ? (
              <Button variant="ghost" onClick={back} disabled={busy}>
                <ArrowLeft className="h-4 w-4" />
                {t(($) => $.member_setup.back)}
              </Button>
            ) : (
              <span />
            )}
            <Button size="lg" onClick={() => void next()} disabled={busy || (isLast && !destination)}>
              {busy && <Loader2 className="h-4 w-4 animate-spin" />}
              {cta}
              {!isLast && <ArrowRight className="h-4 w-4" />}
            </Button>
          </footer>
        </div>
      </div>
    </div>
  );
}
