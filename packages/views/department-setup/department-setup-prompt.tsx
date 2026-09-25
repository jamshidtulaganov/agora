"use client";

import { useMemo } from "react";
import { X } from "lucide-react";
import { toast } from "sonner";
import { useProductTourStore, useWelcomeStore } from "@agora/core/onboarding";
import { paths, useCurrentWorkspace } from "@agora/core/paths";
import { useCurrentMember } from "@agora/core/permissions";
import type { Workspace } from "@agora/core/types";
import {
  readDepartmentSetup,
  useSetDepartmentSetup,
} from "@agora/core/workspace/department-setup";
import { useSetupPromptStore } from "@agora/core/workspace/setup-prompt-store";
import { Button } from "@agora/ui/components/ui/button";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";

/**
 * A small card in the corner that offers owners and admins the department
 * setup until someone decides — "Set up" opens the wizard, "Use the default
 * setup" records the choice. Never a blocking modal, and it waits for the
 * first-login welcome and product tour to finish. Closing it only hides it
 * for this session.
 */
export function DepartmentSetupPrompt() {
  const workspace = useCurrentWorkspace();
  if (!workspace) return null;
  return <PromptGate workspace={workspace} />;
}

function PromptGate({ workspace }: { workspace: Workspace }) {
  const { role } = useCurrentMember(workspace.id);
  const decided = useMemo(() => readDepartmentSetup(workspace.settings) !== null, [workspace.settings]);
  // Mirrors the render conditions of ProductTour and WelcomeAfterOnboarding:
  // both only show inside the workspace their signal points at.
  const tourActive = useProductTourStore((s) => s.workspaceId === workspace.id);
  const welcomeActive = useWelcomeStore(
    (s) => s.signal?.workspaceId === workspace.id && !s.dismissed,
  );
  const dismissed = useSetupPromptStore((s) => s.dismissed[workspace.id] === true);
  const { pathname } = useNavigation();
  const setupPath = paths.workspace(workspace.slug).setup();
  const onSetupPage = pathname === setupPath || pathname.startsWith(`${setupPath}/`);

  const isAdmin = role === "owner" || role === "admin";
  if (!isAdmin || decided || tourActive || welcomeActive || dismissed || onSetupPage) return null;
  return <PromptCard workspace={workspace} setupPath={setupPath} />;
}

function PromptCard({ workspace, setupPath }: { workspace: Workspace; setupPath: string }) {
  const { t } = useT("department-setup");
  const navigation = useNavigation();
  const setDepartmentSetup = useSetDepartmentSetup();
  const dismiss = useSetupPromptStore((s) => s.dismiss);

  const takeDefault = () =>
    setDepartmentSetup.mutate(
      { workspaceId: workspace.id, status: "skipped" },
      { onError: () => toast.error(t(($) => $.actions.default_failed)) },
    );

  return (
    // Above the Assistant button that sits in the same corner.
    <aside
      aria-labelledby="setup-prompt-title"
      data-testid="setup-prompt"
      className="fixed bottom-16 right-4 z-40 w-80 max-w-[calc(100vw-2rem)] rounded-lg border bg-popover p-4 text-popover-foreground shadow-lg"
    >
      <div className="flex items-start justify-between gap-2">
        <h2 id="setup-prompt-title" className="text-sm font-semibold">
          {t(($) => $.prompt.title)}
        </h2>
        <button
          type="button"
          onClick={() => dismiss(workspace.id)}
          aria-label={t(($) => $.prompt.dismiss)}
          data-testid="setup-prompt-dismiss"
          className="-mr-1 -mt-1 rounded-md p-1 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <X className="size-3.5" aria-hidden />
        </button>
      </div>
      <p className="mt-1 text-sm text-muted-foreground">{t(($) => $.prompt.description)}</p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={() => navigation.push(setupPath)} data-testid="setup-prompt-start">
          {t(($) => $.prompt.start)}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          className="text-muted-foreground"
          onClick={takeDefault}
          data-testid="setup-prompt-default"
        >
          {t(($) => $.prompt.use_default)}
        </Button>
      </div>
    </aside>
  );
}
