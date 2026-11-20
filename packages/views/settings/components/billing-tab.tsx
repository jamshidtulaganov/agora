"use client";

import { useQuery } from "@tanstack/react-query";
import { Badge } from "@agora/ui/components/ui/badge";
import { Button } from "@agora/ui/components/ui/button";
import { Card, CardContent } from "@agora/ui/components/ui/card";
import { useWorkspaceId } from "@agora/core/hooks";
import {
  isUnlimited,
  workspaceSubscriptionOptions,
  type SubscriptionPlan,
  type WorkspaceSubscription,
} from "@agora/core/billing";
import { useT } from "../../i18n";

const PAID_PLANS = new Set(["team", "enterprise"]);

function formatDate(iso: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  // UTC: paid-through dates are stored as the end of that day in UTC, so a
  // local zone east of UTC would otherwise show the following day.
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleDateString(undefined, { timeZone: "UTC" });
}

function formatDollars(cents: number): string {
  return (cents / 100).toFixed(cents % 100 === 0 ? 0 : 2);
}

function contactHref(contact: string): string {
  if (/^https?:\/\//i.test(contact)) return contact;
  if (contact.includes("@")) return `mailto:${contact}`;
  return "";
}

export function BillingTab() {
  const wsId = useWorkspaceId();
  const { data: sub, isLoading } = useQuery(workspaceSubscriptionOptions(wsId));
  const { t } = useT("settings");

  if (isLoading || !sub) {
    return <div className="text-sm text-muted-foreground">{t(($) => $.billing.loading)}</div>;
  }

  return (
    <div className="space-y-6">
      <h2 className="text-sm font-semibold">{t(($) => $.billing.title)}</h2>
      <PlanCard sub={sub} />
      <UsageCard sub={sub} />
      {!(PAID_PLANS.has(sub.plan) && sub.status === "active") && <UpgradeCard sub={sub} />}
    </div>
  );
}

function usePlanName() {
  const { t } = useT("settings");
  return (id: string) => {
    switch (id) {
      case "free":
        return t(($) => $.billing.plans.free);
      case "team":
        return t(($) => $.billing.plans.team);
      case "enterprise":
        return t(($) => $.billing.plans.enterprise);
      default:
        return id || t(($) => $.billing.plans.team);
    }
  };
}

function PlanCard({ sub }: { sub: WorkspaceSubscription }) {
  const { t } = useT("settings");
  const planName = usePlanName();

  let line = "";
  switch (sub.status) {
    case "trialing":
      line = t(($) => $.billing.trial_ends, { date: formatDate(sub.trial_ends_at) });
      break;
    case "active":
      line = sub.current_period_end
        ? t(($) => $.billing.paid_through, { date: formatDate(sub.current_period_end) })
        : "";
      break;
    case "past_due":
      line = t(($) => $.billing.past_due, { date: formatDate(sub.grace_ends_at) });
      break;
    case "trial_expired":
      line = t(($) => $.billing.trial_expired);
      break;
    case "expired":
      line = t(($) => $.billing.expired);
      break;
    case "canceled":
      line = t(($) => $.billing.canceled);
      break;
    default:
      line = "";
  }

  return (
    <Card>
      <CardContent className="space-y-2">
        <div className="flex items-center gap-2">
          <span className="text-base font-semibold">{planName(sub.plan)}</span>
          {sub.status === "trialing" && (
            <Badge variant="secondary">
              {t(($) => $.billing.trial_badge)} · {t(($) => $.billing.days_left, { days: sub.trial_days_left })}
            </Badge>
          )}
        </div>
        {line && <p className="text-sm text-muted-foreground">{line}</p>}
        {sub.plan === "free" && (
          <p className="text-sm text-muted-foreground">{t(($) => $.billing.free_note)}</p>
        )}
      </CardContent>
    </Card>
  );
}

function UsageCard({ sub }: { sub: WorkspaceSubscription }) {
  const { t } = useT("settings");
  const rows: { key: string; label: string; used: number; limit: number }[] = [
    { key: "automations", label: t(($) => $.billing.usage.automations), used: sub.usage.automations, limit: sub.limits.automations },
    { key: "report_schedules", label: t(($) => $.billing.usage.report_schedules), used: sub.usage.report_schedules, limit: sub.limits.report_schedules },
    { key: "autopilots", label: t(($) => $.billing.usage.autopilots), used: sub.usage.autopilots, limit: sub.limits.autopilots },
  ];

  return (
    <Card>
      <CardContent className="space-y-3">
        <div className="flex items-center justify-between gap-3 text-sm">
          <span>{t(($) => $.billing.usage.members)}</span>
          <span className="shrink-0 whitespace-nowrap tabular-nums text-muted-foreground">{sub.usage.seats}</span>
        </div>
        {rows.map((row) => {
          const over = !isUnlimited(row.limit) && row.used > row.limit;
          return (
            <div key={row.key} className="space-y-0.5">
              <div className="flex items-center justify-between gap-3 text-sm">
                <span>{row.label}</span>
                <span className="shrink-0 whitespace-nowrap tabular-nums text-muted-foreground">
                  {isUnlimited(row.limit)
                    ? `${row.used} · ${t(($) => $.billing.unlimited)}`
                    : row.limit === 0
                      ? `${row.used} · ${t(($) => $.billing.team_only)}`
                      : t(($) => $.billing.usage_of, { used: row.used, limit: row.limit })}
                </span>
              </div>
              {over && <p className="text-xs text-muted-foreground">{t(($) => $.billing.over_limit)}</p>}
            </div>
          );
        })}
        <p className="text-xs text-muted-foreground">{t(($) => $.billing.agents_free)}</p>
      </CardContent>
    </Card>
  );
}

function UpgradeCard({ sub }: { sub: WorkspaceSubscription }) {
  const { t } = useT("settings");
  const team: SubscriptionPlan | undefined = sub.plans.find((p) => p.id === "team");
  const href = contactHref(sub.contact);

  return (
    <Card>
      <CardContent className="space-y-3">
        <div className="space-y-1">
          <div className="text-sm font-semibold">{t(($) => $.billing.plans.team)}</div>
          {team && team.price_yearly_cents > 0 && (
            <p className="text-sm">
              {t(($) => $.billing.team_price, {
                yearly: formatDollars(team.price_yearly_cents),
                monthly: formatDollars(team.price_monthly_cents),
              })}
            </p>
          )}
          <p className="text-sm text-muted-foreground">{t(($) => $.billing.team_features)}</p>
        </div>
        {href ? (
          <Button size="sm" nativeButton={false} render={<a href={href} target="_blank" rel="noreferrer" />}>
            {t(($) => $.billing.upgrade)}
          </Button>
        ) : (
          <p className="text-sm text-muted-foreground">
            {sub.contact || t(($) => $.billing.upgrade_no_contact)}
          </p>
        )}
      </CardContent>
    </Card>
  );
}
