"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { RefreshCw, Search } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { Input } from "@agora/ui/components/ui/input";
import { Label } from "@agora/ui/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@agora/ui/components/ui/native-select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@agora/ui/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@agora/ui/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@agora/ui/components/ui/dialog";
import { ApiError } from "@agora/core/api";
import {
  adminOverviewOptions,
  useSetAdminWorkspacePlan,
  type AdminOverview,
  type AdminPlanChange,
  type AdminUser,
  type AdminWorkspace,
} from "@agora/core/platform-admin";
import { useT, useTimeAgo } from "../i18n";
import { GrowthView, MarketingView, RangePicker, type Range } from "./growth-view";

function formatDate(iso: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleDateString(undefined, { timeZone: "UTC" });
}

function formatMoney(cents: number): string {
  return `$${(cents / 100).toLocaleString(undefined, { maximumFractionDigits: 0 })}`;
}

function matches(query: string, ...fields: string[]): boolean {
  const q = query.trim().toLowerCase();
  return q === "" || fields.some((f) => f.toLowerCase().includes(q));
}

/**
 * Platform admin dashboard (/admin): every user and workspace on this Agora,
 * with plans and manual plan changes. The API answers 404 to anyone who is not
 * a platform admin, and the page shows "not found" in that case.
 */
export function AdminPage() {
  const { t } = useT("admin");
  const { data, isLoading, error, refetch, isFetching } = useQuery(adminOverviewOptions());
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState("growth");
  const [range, setRange] = useState<Range>(30);

  if (isLoading) {
    return <Shell><p className="text-sm text-muted-foreground">{t(($) => $.loading)}</p></Shell>;
  }
  if (error || !data) {
    const notFound = error instanceof ApiError && error.status === 404;
    return (
      <Shell>
        <p className="text-sm text-muted-foreground">
          {notFound ? t(($) => $.not_found) : t(($) => $.error)}
        </p>
      </Shell>
    );
  }

  return (
    <Shell>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-lg font-semibold">{t(($) => $.title)}</h1>
          <p className="text-sm text-muted-foreground">{t(($) => $.subtitle)}</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
          <RefreshCw className={isFetching ? "size-4 animate-spin" : "size-4"} />
          {t(($) => $.refresh)}
        </Button>
      </div>

      <SummaryCards summary={data.summary} />

      <Tabs value={tab} onValueChange={(v) => setTab(String(v))} className="gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <TabsList>
            <TabsTrigger value="growth">{t(($) => $.tabs.growth)}</TabsTrigger>
            <TabsTrigger value="marketing">{t(($) => $.tabs.marketing)}</TabsTrigger>
            <TabsTrigger value="workspaces">
              {t(($) => $.tabs.workspaces)} · {data.workspaces.length}
            </TabsTrigger>
            <TabsTrigger value="users">
              {t(($) => $.tabs.users)} · {data.users.length}
            </TabsTrigger>
          </TabsList>
          {tab === "growth" || tab === "marketing" ? (
            <RangePicker value={range} onChange={setRange} />
          ) : (
            <div className="relative w-full sm:w-72">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder={t(($) => $.search_placeholder)}
                className="pl-8"
              />
            </div>
          )}
        </div>
        <TabsContent value="growth">
          <GrowthView range={range} />
        </TabsContent>
        <TabsContent value="marketing">
          <MarketingView range={range} />
        </TabsContent>
        <TabsContent value="workspaces">
          <WorkspacesTable
            workspaces={data.workspaces.filter((w) => matches(query, w.name, w.slug, w.owner_email))}
          />
        </TabsContent>
        <TabsContent value="users">
          <UsersTable
            users={data.users.filter((u) =>
              matches(query, u.name, u.email, ...u.workspaces.map((w) => w.name)),
            )}
          />
        </TabsContent>
      </Tabs>
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-svh bg-background">
      <div className="mx-auto w-full max-w-6xl space-y-6 p-4 md:p-8">{children}</div>
    </div>
  );
}

function SummaryCards({ summary }: { summary: AdminOverview["summary"] }) {
  const { t } = useT("admin");
  const cards: { label: string; value: string; hint?: string }[] = [
    {
      label: t(($) => $.summary.users),
      value: String(summary.users),
      hint: `${t(($) => $.summary.new_7d)}: ${summary.new_users_7d}`,
    },
    {
      label: t(($) => $.summary.active_7d),
      value: String(summary.active_users_7d),
      hint: t(($) => $.summary.of, { total: summary.users }),
    },
    {
      label: t(($) => $.summary.workspaces),
      value: String(summary.workspaces),
      hint: `${t(($) => $.summary.free)}: ${summary.free}`,
    },
    {
      label: t(($) => $.summary.trialing),
      value: String(summary.trialing),
      hint: `${t(($) => $.summary.trials_ending)}: ${summary.trials_ending_7d}`,
    },
    {
      label: t(($) => $.summary.paying),
      value: String(summary.paying),
      hint: `${t(($) => $.summary.past_due)}: ${summary.past_due}`,
    },
    {
      label: t(($) => $.summary.revenue),
      value: formatMoney(summary.est_monthly_revenue_cents),
    },
  ];
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
      {cards.map((card) => (
        <Card key={card.label} size="sm">
          <CardContent className="space-y-1">
            <p className="truncate text-xs text-muted-foreground" title={card.label}>{card.label}</p>
            <p className="text-2xl font-semibold tabular-nums">{card.value}</p>
            {card.hint && <p className="truncate text-xs text-muted-foreground" title={card.hint}>{card.hint}</p>}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function PlanBadge({ workspace }: { workspace: AdminWorkspace }) {
  const { t } = useT("admin");
  const sub = workspace.subscription;
  const planLabel =
    sub.plan === "free" ? t(($) => $.plan.free)
      : sub.plan === "team" ? t(($) => $.plan.team)
        : sub.plan === "enterprise" ? t(($) => $.plan.enterprise)
          : sub.plan || "—";
  let statusLabel = "";
  switch (sub.status) {
    case "trialing": statusLabel = t(($) => $.status.trialing); break;
    case "active": statusLabel = t(($) => $.status.active); break;
    case "past_due": statusLabel = t(($) => $.status.past_due); break;
    case "trial_expired": statusLabel = t(($) => $.status.trial_expired); break;
    case "expired": statusLabel = t(($) => $.status.expired); break;
    case "canceled": statusLabel = t(($) => $.status.canceled); break;
    default: statusLabel = sub.status;
  }
  const attention = sub.status === "past_due" || sub.status === "trial_expired" || sub.status === "expired";
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Badge variant={sub.plan === "free" ? "outline" : "secondary"}>{planLabel}</Badge>
      {statusLabel && (
        <span className={attention ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
          {statusLabel}
        </span>
      )}
      {workspace.provider === "grandfathered" && (
        <span className="text-xs text-muted-foreground">· {t(($) => $.workspaces.grandfathered)}</span>
      )}
    </div>
  );
}

function WorkspacesTable({ workspaces }: { workspaces: AdminWorkspace[] }) {
  const { t } = useT("admin");
  const timeAgo = useTimeAgo();
  const [editing, setEditing] = useState<AdminWorkspace | null>(null);

  if (workspaces.length === 0) {
    return <p className="py-8 text-center text-sm text-muted-foreground">{t(($) => $.workspaces.empty)}</p>;
  }
  return (
    <Card size="sm" className="py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t(($) => $.workspaces.name)}</TableHead>
            <TableHead>{t(($) => $.workspaces.plan)}</TableHead>
            <TableHead>{t(($) => $.workspaces.ends)}</TableHead>
            <TableHead className="text-right">{t(($) => $.workspaces.members)}</TableHead>
            <TableHead>{t(($) => $.workspaces.last_active)}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {workspaces.map((w) => {
            const sub = w.subscription;
            const ends =
              sub.status === "trialing"
                ? `${formatDate(sub.trial_ends_at)} · ${t(($) => $.workspaces.days_left, { days: sub.trial_days_left })}`
                : sub.status === "trial_expired"
                  ? formatDate(sub.trial_ends_at)
                  : formatDate(sub.current_period_end);
            return (
              <TableRow key={w.id}>
                <TableCell className="max-w-64">
                  <div className="truncate font-medium" title={w.name}>{w.name}</div>
                  <div className="truncate text-xs text-muted-foreground" title={w.owner_email}>
                    {w.slug}{w.owner_email ? ` · ${w.owner_email}` : ""}
                  </div>
                  {w.note && <div className="truncate text-xs text-muted-foreground" title={w.note}>{w.note}</div>}
                </TableCell>
                <TableCell><PlanBadge workspace={w} /></TableCell>
                <TableCell className="whitespace-nowrap text-muted-foreground">{ends}</TableCell>
                <TableCell className="text-right tabular-nums">{w.members}</TableCell>
                <TableCell className="whitespace-nowrap text-muted-foreground">
                  {w.last_active_at ? timeAgo(w.last_active_at) : t(($) => $.users.never)}
                </TableCell>
                <TableCell className="text-right">
                  <Button variant="outline" size="sm" onClick={() => setEditing(w)}>
                    {t(($) => $.workspaces.change)}
                  </Button>
                </TableCell>
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
      {editing && <ChangePlanDialog workspace={editing} onClose={() => setEditing(null)} />}
    </Card>
  );
}

function UsersTable({ users }: { users: AdminUser[] }) {
  const { t } = useT("admin");
  const timeAgo = useTimeAgo();
  if (users.length === 0) {
    return <p className="py-8 text-center text-sm text-muted-foreground">{t(($) => $.users.empty)}</p>;
  }
  return (
    <Card size="sm" className="py-0">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t(($) => $.users.name)}</TableHead>
            <TableHead>{t(($) => $.users.workspaces)}</TableHead>
            <TableHead>{t(($) => $.users.signed_up)}</TableHead>
            <TableHead>{t(($) => $.users.last_login)}</TableHead>
            <TableHead>{t(($) => $.users.last_active)}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {users.map((u) => (
            <TableRow key={u.id}>
              <TableCell className="max-w-64">
                <div className="truncate font-medium" title={u.name}>{u.name || "—"}</div>
                <div className="truncate text-xs text-muted-foreground" title={u.email}>{u.email}</div>
              </TableCell>
              <TableCell className="max-w-72">
                <div className="truncate text-muted-foreground" title={u.workspaces.map((w) => `${w.name} (${w.role})`).join(", ")}>
                  {u.workspaces.length === 0 ? "—" : u.workspaces.map((w) => w.name).join(", ")}
                </div>
              </TableCell>
              <TableCell className="whitespace-nowrap text-muted-foreground">{formatDate(u.created_at)}</TableCell>
              <TableCell className="whitespace-nowrap text-muted-foreground">
                {u.last_login_at ? timeAgo(u.last_login_at) : t(($) => $.users.never)}
              </TableCell>
              <TableCell className="whitespace-nowrap text-muted-foreground">
                {u.last_active_at ? timeAgo(u.last_active_at) : t(($) => $.users.never)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}

function dateInputValue(iso: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toISOString().slice(0, 10);
}

function ChangePlanDialog({ workspace, onClose }: { workspace: AdminWorkspace; onClose: () => void }) {
  const { t } = useT("admin");
  const sub = workspace.subscription;
  const initialMode: AdminPlanChange["mode"] =
    sub.status === "trialing" ? "trial"
      : sub.purchased_plan === "enterprise" ? "enterprise"
        : sub.plan === "free" ? "free"
          : "team";
  const [mode, setMode] = useState<AdminPlanChange["mode"]>(initialMode);
  const [billingInterval, setBillingInterval] = useState<"" | "month" | "year">(
    sub.billing_interval === "month" || sub.billing_interval === "year" ? sub.billing_interval : "",
  );
  const [seats, setSeats] = useState(String(sub.seats_paid || workspace.members || 0));
  const [until, setUntil] = useState(dateInputValue(sub.current_period_end));
  const [trialUntil, setTrialUntil] = useState(dateInputValue(sub.trial_ends_at));
  const [note, setNote] = useState(workspace.note);
  const setPlan = useSetAdminWorkspacePlan();

  const paid = mode === "team" || mode === "enterprise";
  const canSave = useMemo(() => (mode === "trial" ? trialUntil !== "" : true), [mode, trialUntil]);

  const save = () => {
    const change: AdminPlanChange = { mode, note };
    if (paid) {
      change.interval = billingInterval;
      change.seats = Math.max(0, Number.parseInt(seats, 10) || 0);
      change.until = until;
    }
    if (mode === "trial") change.trial_until = trialUntil;
    setPlan.mutate(
      { workspaceId: workspace.id, change },
      {
        onSuccess: () => {
          toast.success(t(($) => $.dialog.saved));
          onClose();
        },
        onError: (err) => {
          toast.error(err instanceof Error && err.message ? err.message : t(($) => $.dialog.failed));
        },
      },
    );
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="truncate">{t(($) => $.dialog.title, { name: workspace.name })}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="admin-plan-mode">{t(($) => $.dialog.mode)}</Label>
            <NativeSelect
              id="admin-plan-mode"
              className="w-full"
              value={mode}
              onChange={(e) => setMode(e.target.value as AdminPlanChange["mode"])}
            >
              <NativeSelectOption value="team">{t(($) => $.dialog.modes.team)}</NativeSelectOption>
              <NativeSelectOption value="enterprise">{t(($) => $.dialog.modes.enterprise)}</NativeSelectOption>
              <NativeSelectOption value="trial">{t(($) => $.dialog.modes.trial)}</NativeSelectOption>
              <NativeSelectOption value="free">{t(($) => $.dialog.modes.free)}</NativeSelectOption>
            </NativeSelect>
          </div>

          {paid && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="admin-plan-interval">{t(($) => $.dialog.interval)}</Label>
                <NativeSelect
                  id="admin-plan-interval"
                  className="w-full"
                  value={billingInterval}
                  onChange={(e) => setBillingInterval(e.target.value as "" | "month" | "year")}
                >
                  <NativeSelectOption value="">{t(($) => $.dialog.interval_none)}</NativeSelectOption>
                  <NativeSelectOption value="month">{t(($) => $.dialog.month)}</NativeSelectOption>
                  <NativeSelectOption value="year">{t(($) => $.dialog.year)}</NativeSelectOption>
                </NativeSelect>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="admin-plan-seats">{t(($) => $.dialog.seats)}</Label>
                <Input
                  id="admin-plan-seats"
                  type="number"
                  min={0}
                  value={seats}
                  onChange={(e) => setSeats(e.target.value)}
                />
              </div>
              <div className="col-span-2 space-y-1.5">
                <Label htmlFor="admin-plan-until">{t(($) => $.dialog.until)}</Label>
                <Input id="admin-plan-until" type="date" value={until} onChange={(e) => setUntil(e.target.value)} />
                <p className="text-xs text-muted-foreground">{t(($) => $.dialog.until_hint)}</p>
              </div>
            </div>
          )}

          {mode === "trial" && (
            <div className="space-y-1.5">
              <Label htmlFor="admin-plan-trial">{t(($) => $.dialog.trial_until)}</Label>
              <Input id="admin-plan-trial" type="date" value={trialUntil} onChange={(e) => setTrialUntil(e.target.value)} />
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="admin-plan-note">{t(($) => $.dialog.note)}</Label>
            <Input
              id="admin-plan-note"
              value={note}
              maxLength={500}
              placeholder={t(($) => $.dialog.note_placeholder)}
              onChange={(e) => setNote(e.target.value)}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>{t(($) => $.dialog.cancel)}</Button>
          <Button onClick={save} disabled={!canSave || setPlan.isPending}>{t(($) => $.dialog.save)}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
