"use client";

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { ClipboardList } from "lucide-react";
import { issueTimelineOptions } from "@agora/core/issues/queries";
import { latestDesignAudit } from "@agora/core/design";
import { useT } from "../../i18n";

// Read-only design-system audit report, rendered on the audit chore issue (or
// any issue carrying a ```design-audit``` block): off-token values, duplicated
// markup, unmanaged components, and proposed tokens. Renders nothing when the
// issue has no audit.
export function DesignAuditSection({ issueId }: { issueId: string }) {
  const { t } = useT("issues");
  const { data: timeline = [] } = useQuery(issueTimelineOptions(issueId));

  const audit = useMemo(() => {
    const comments = timeline
      .filter((e) => e.type === "comment")
      .map((e) => ({ author_type: e.actor_type, content: e.content ?? "", created_at: e.created_at }));
    return latestDesignAudit(comments);
  }, [timeline]);

  if (!audit) return null;

  const empty =
    audit.off_token.length === 0 &&
    audit.duplicates.length === 0 &&
    audit.unmanaged_components.length === 0 &&
    audit.proposed_tokens.length === 0;

  return (
    <div>
      <div className="mb-2 flex items-center gap-1 px-2 py-1 text-xs font-medium">
        <ClipboardList className="!size-3 shrink-0 text-muted-foreground" />
        {t(($) => $.design_audit.title)}
      </div>
      <div className="space-y-3 pl-2 text-xs">
        {audit.summary && <p className="text-muted-foreground">{audit.summary}</p>}
        {empty && <p className="text-muted-foreground">{t(($) => $.design_audit.clean)}</p>}

        {audit.off_token.length > 0 && (
          <AuditGroup title={t(($) => $.design_audit.off_token)}>
            {audit.off_token.map((o, i) => (
              <li key={i} className="flex items-center gap-1.5 px-3 py-1.5">
                <span className="rounded bg-amber-500/15 px-1 py-0.5 text-[9px] font-medium uppercase text-amber-600 dark:text-amber-400">
                  {o.kind}
                </span>
                <code className="font-mono">{o.value}</code>
                <span className="text-muted-foreground">×{o.occurrences}</span>
                {o.suggested_token && (
                  <span className="ml-auto text-emerald-600 dark:text-emerald-400">→ {o.suggested_token}</span>
                )}
              </li>
            ))}
          </AuditGroup>
        )}

        {audit.duplicates.length > 0 && (
          <AuditGroup title={t(($) => $.design_audit.duplicates)}>
            {audit.duplicates.map((d, i) => (
              <li key={i} className="flex items-center gap-1.5 px-3 py-1.5">
                <span className="truncate">{d.pattern}</span>
                <span className="text-muted-foreground">×{d.occurrences}</span>
                {d.suggested_component && (
                  <span className="ml-auto text-emerald-600 dark:text-emerald-400">→ {d.suggested_component}</span>
                )}
              </li>
            ))}
          </AuditGroup>
        )}

        {audit.unmanaged_components.length > 0 && (
          <AuditGroup title={t(($) => $.design_audit.unmanaged)}>
            {audit.unmanaged_components.map((u, i) => (
              <li key={i} className="flex flex-col gap-0.5 px-3 py-1.5">
                <span className="font-medium">{u.name}</span>
                {u.code_ref && <code className="text-[10px] text-muted-foreground">{u.code_ref}</code>}
              </li>
            ))}
          </AuditGroup>
        )}

        {audit.proposed_tokens.length > 0 && (
          <AuditGroup title={t(($) => $.design_audit.proposed_tokens)}>
            {audit.proposed_tokens.map((p, i) => (
              <li key={i} className="flex items-center gap-1.5 px-3 py-1.5">
                <span className="font-medium">{p.name}</span>
                <code className="font-mono text-muted-foreground">{p.value}</code>
                {p.replaces.length > 0 && (
                  <span className="ml-auto text-[10px] text-muted-foreground">
                    {t(($) => $.design_audit.replaces, { count: p.replaces.length })}
                  </span>
                )}
              </li>
            ))}
          </AuditGroup>
        )}
      </div>
    </div>
  );
}

function AuditGroup({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section>
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">{title}</div>
      <ul className="divide-y divide-border rounded-md border">{children}</ul>
    </section>
  );
}
