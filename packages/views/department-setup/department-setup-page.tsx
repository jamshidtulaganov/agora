"use client";

import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Compass, PanelRight } from "lucide-react";
import { toast } from "sonner";
import { api } from "@agora/core/api";
import { useWorkspaceId } from "@agora/core/hooks";
import { knowledgeListOptions, type KnowledgeDoc } from "@agora/core/knowledge";
import { autopilotKeys } from "@agora/core/autopilots/queries";
import { useCurrentWorkspace, useWorkspacePaths } from "@agora/core/paths";
import { useCurrentMember } from "@agora/core/permissions";
import type { Workspace } from "@agora/core/types";
import {
  readTeamSidebar,
  useSetDepartmentSetup,
  useUpdateTeamSidebar,
} from "@agora/core/workspace/department-setup";
import { workspaceKeys } from "@agora/core/workspace/queries";
import { Button, buttonVariants } from "@agora/ui/components/ui/button";
import { Skeleton } from "@agora/ui/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@agora/ui/components/ui/tooltip";
import { useViewingTimezone } from "../common/use-viewing-timezone";
import { CockpitFrame, type CockpitRailToggle } from "../layout/cockpit-frame";
import { PageHeader } from "../layout/page-header";
import { useNavigation } from "../navigation";
import { useT } from "../i18n";
import { AgentsStep, useSetupAgentsData } from "./agents-step";
import { DoneStep } from "./done-step";
import { KnowledgeStep } from "./knowledge-step";
import { presetHiddenNav } from "./presets";
import { createSetupAgents, type SetupAgentResult } from "./setup-agents";
import { SetupSummary } from "./setup-summary";
import { SidebarStep } from "./sidebar-step";
import { TeamSidebarPreview } from "./team-sidebar-preview";

const STEPS = ["knowledge", "sidebar", "agents"] as const;
const EMPTY_DOCS: KnowledgeDoc[] = [];
type StepId = (typeof STEPS)[number];

function errorText(err: unknown): string | undefined {
  return err instanceof Error && err.message ? err.message : undefined;
}

/**
 * Department setup: an owner or admin sets the workspace up for their team —
 * knowledge, what the team's sidebar shows, a few ready-made agents — or
 * takes the default Agora setup with one click from any step. Finishing or
 * skipping is recorded on the workspace so nobody is prompted again; the page
 * stays reachable from Settings → Workspace to redo it.
 */
export function DepartmentSetupPage() {
  const wsId = useWorkspaceId();
  const workspace = useCurrentWorkspace();
  const { role, isLoading: roleLoading } = useCurrentMember(wsId);
  const knowledge = useQuery(knowledgeListOptions(wsId));
  // Two signals, so a drifted member list can't lock an admin out: the
  // caller's role, and the knowledge endpoint's own can_manage (the server
  // still answers 403 to anyone else).
  const canManage = role === "owner" || role === "admin" || knowledge.data?.can_manage === true;

  if (roleLoading && !canManage) {
    return (
      <SetupShell>
        <div className="space-y-3">
          <Skeleton className="h-6 w-1/2" />
          <Skeleton className="h-32 w-full" />
        </div>
      </SetupShell>
    );
  }
  if (!canManage) {
    return (
      <SetupShell>
        <MembersNotice />
      </SetupShell>
    );
  }
  if (!workspace) return <SetupShell>{null}</SetupShell>;
  return (
    <SetupWizard
      workspace={workspace}
      documents={knowledge.data?.documents ?? EMPTY_DOCS}
      documentsLoading={knowledge.isLoading}
    />
  );
}

/**
 * The page frame every state shares, laid out like issue detail: the header
 * and content fill the width, an optional rail docks to the right edge
 * (resizable, a sheet on mobile), and an optional action bar is pinned under
 * the content so Continue sits in the same place on every step.
 */
function SetupShell({
  headerRight,
  aside,
  footer,
  children,
}: {
  headerRight?: React.ReactNode;
  aside?: React.ReactNode;
  footer?: React.ReactNode;
  children: React.ReactNode;
}) {
  const { t } = useT("department-setup");
  const header = (rail?: CockpitRailToggle) => (
    <PageHeader className="px-5">
      <div className="flex items-center gap-2">
        <Compass className="h-4 w-4 text-muted-foreground" aria-hidden />
        <h1 className="text-sm font-medium">{t(($) => $.page.title)}</h1>
      </div>
      <div className="ml-auto flex items-center gap-3">
        {headerRight}
        {rail && (
          <Tooltip>
            <TooltipTrigger
              className={buttonVariants({
                variant: rail.open ? "secondary" : "ghost",
                size: "icon-sm",
                className: rail.open ? "" : "text-muted-foreground",
              })}
              onClick={rail.toggle}
              aria-label={t(($) => $.summary.title)}
            >
              <PanelRight />
            </TooltipTrigger>
            <TooltipContent side="bottom">{t(($) => $.summary.title)}</TooltipContent>
          </Tooltip>
        )}
      </div>
    </PageHeader>
  );
  const body = (
    <>
      <div className="flex-1 overflow-y-auto">
        <div className="w-full px-8 py-8">{children}</div>
      </div>
      {footer && (
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t bg-background px-8 py-3">
          {footer}
        </div>
      )}
    </>
  );

  if (!aside) {
    return (
      <div className="flex h-full flex-col">
        {header()}
        {body}
      </div>
    );
  }
  return (
    <div className="flex h-full flex-col">
      <CockpitFrame layoutId="department-setup" header={(rail) => header(rail)} rail={aside}>
        {body}
      </CockpitFrame>
    </div>
  );
}

function MembersNotice() {
  const { t } = useT("department-setup");
  const navigation = useNavigation();
  const p = useWorkspacePaths();
  return (
    <div className="space-y-3" data-testid="setup-members-only">
      <h2 className="text-base font-semibold">{t(($) => $.members.title)}</h2>
      <p className="text-sm text-muted-foreground">{t(($) => $.members.description)}</p>
      <Button variant="outline" size="sm" onClick={() => navigation.push(p.issues())}>
        {t(($) => $.members.open_issues)}
      </Button>
    </div>
  );
}

function SetupWizard({
  workspace,
  documents,
  documentsLoading,
}: {
  workspace: Workspace;
  documents: KnowledgeDoc[];
  documentsLoading: boolean;
}) {
  const { t } = useT("department-setup");
  const navigation = useNavigation();
  const p = useWorkspacePaths();
  const qc = useQueryClient();
  const timezone = useViewingTimezone();
  const updateTeamSidebar = useUpdateTeamSidebar();
  const setDepartmentSetup = useSetDepartmentSetup();
  const agentsData = useSetupAgentsData(workspace.id);

  const [step, setStep] = useState<StepId | "done">("knowledge");
  // Start from the team sidebar already saved (redoing the setup), else Simple.
  const [hidden, setHidden] = useState<string[]>(
    () => readTeamSidebar(workspace.settings) ?? presetHiddenNav("simple"),
  );
  const [selected, setSelected] = useState<string[]>([]);
  const [results, setResults] = useState<SetupAgentResult[]>([]);
  const [busy, setBusy] = useState(false);

  const takeDefault = () => {
    // Optimistic: the prompt disappears at once; a failed save only needs a
    // toast, the person can pick again from the prompt.
    setDepartmentSetup.mutate(
      { workspaceId: workspace.id, status: "skipped" },
      { onError: () => toast.error(t(($) => $.actions.default_failed)) },
    );
    navigation.push(p.issues());
  };

  const saveSidebar = async () => {
    setBusy(true);
    try {
      await updateTeamSidebar.mutateAsync({ workspaceId: workspace.id, hidden });
      setStep("agents");
    } catch (err) {
      toast.error(t(($) => $.sidebar.save_failed), { description: errorText(err) });
    } finally {
      setBusy(false);
    }
  };

  const finish = async () => {
    setBusy(true);
    const chosen = agentsData.templates.filter(
      (template) => selected.includes(template.slug) && !agentsData.existing.has(template.slug),
    );
    let created: SetupAgentResult[] = [];
    if (agentsData.runtime && chosen.length > 0) {
      created = await createSetupAgents(api, {
        templates: chosen,
        runtimeId: agentsData.runtime.id,
        timezone,
        digest: {
          title: t(($) => $.autopilot.title),
          description: t(($) => $.autopilot.description),
          issueTitle: t(($) => $.autopilot.issue_title),
          scheduleLabel: t(($) => $.autopilot.schedule_label),
        },
      });
      void qc.invalidateQueries({ queryKey: workspaceKeys.agents(workspace.id) });
      void qc.invalidateQueries({ queryKey: autopilotKeys.all(workspace.id) });
    }
    try {
      await setDepartmentSetup.mutateAsync({ workspaceId: workspace.id, status: "done" });
    } catch (err) {
      // The work itself is done; only the "don't ask again" flag didn't stick.
      toast.error(t(($) => $.done.save_failed), { description: errorText(err) });
    }
    setResults(created);
    setBusy(false);
    setStep("done");
  };

  if (step === "done") {
    return (
      <SetupShell aside={<TeamSidebarPreview workspaceName={workspace.name} hidden={hidden} />}>
        <DoneStep
          workspace={workspace}
          documentCount={documents.length}
          teamHidden={hidden}
          results={results}
          onOpenIssues={() => navigation.push(p.issues())}
          onOpenKnowledge={() => navigation.push(p.knowledge())}
        />
      </SetupShell>
    );
  }

  const index = STEPS.indexOf(step);
  const toggleTemplate = (slug: string) =>
    setSelected((prev) => (prev.includes(slug) ? prev.filter((s) => s !== slug) : [...prev, slug]));

  const next = () => {
    if (step === "knowledge") setStep("sidebar");
    else if (step === "sidebar") void saveSidebar();
    else void finish();
  };
  const back = () => setStep(STEPS[Math.max(0, index - 1)] ?? "knowledge");

  const chosenNames = agentsData.templates
    .filter((template) => selected.includes(template.slug) && !agentsData.existing.has(template.slug))
    .map((template) => template.name);

  return (
    <SetupShell
      headerRight={
        <span className="text-xs tabular-nums text-muted-foreground">
          {t(($) => $.page.step_of, { current: index + 1, total: STEPS.length })}
        </span>
      }
      aside={
        <SetupSummary
          steps={STEPS}
          current={index}
          onOpenStep={(target) => !busy && setStep(target)}
          hasInstructions={Boolean(workspace.context?.trim())}
          documentCount={documents.length}
          workspaceName={workspace.name}
          hidden={hidden}
          agentNames={chosenNames}
        />
      }
      footer={
        <>
          <Button
            variant="ghost"
            size="sm"
            className="-ml-2.5 text-muted-foreground"
            onClick={takeDefault}
            disabled={busy}
            data-testid="setup-use-default"
          >
            {t(($) => $.actions.use_default)}
          </Button>
          <div className="flex items-center gap-2">
            {index > 0 && (
              <Button variant="ghost" size="sm" onClick={back} disabled={busy} data-testid="setup-back">
                {t(($) => $.actions.back)}
              </Button>
            )}
            {step === "agents" ? (
              <Button size="sm" onClick={next} disabled={busy} data-testid="setup-finish">
                {busy ? t(($) => $.actions.finishing) : t(($) => $.actions.finish)}
              </Button>
            ) : (
              <Button size="sm" onClick={next} disabled={busy} data-testid="setup-continue">
                {t(($) => $.actions.continue)}
              </Button>
            )}
          </div>
        </>
      }
    >
      <section data-testid={`setup-step-${step}`} aria-labelledby="setup-step-title">
        <h2 id="setup-step-title" className="text-xl font-semibold tracking-tight">
          {t(($) => $[step].title)}
        </h2>
        <p className="mt-1.5 max-w-prose text-sm text-muted-foreground">{t(($) => $[step].description)}</p>

        <div className="mt-7">
          {step === "knowledge" && (
            <KnowledgeStep workspace={workspace} documents={documents} isLoading={documentsLoading} />
          )}
          {step === "sidebar" && <SidebarStep hidden={hidden} onChange={setHidden} disabled={busy} />}
          {step === "agents" && (
            <AgentsStep
              data={agentsData}
              selected={selected}
              onToggle={toggleTemplate}
              runtimesHref={p.runtimes()}
              disabled={busy}
            />
          )}
        </div>
      </section>
    </SetupShell>
  );
}
