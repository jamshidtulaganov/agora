import type { LucideIcon } from "lucide-react";
import type { Issue } from "@agora/core/types";
import { cn } from "@agora/ui/lib/utils";
import { AppLink } from "../../navigation";
import { ActorAvatar } from "../../common/actor-avatar";
import { PriorityIcon } from "../../issues/components/priority-icon";

// The identifier + priority + title + assignee row shared by the QA cockpit's
// list lanes and board cards, so both read as the same surface and carry the
// same "who owns this" signal at a glance.
export function QAIssueRow({ issue }: { issue: Issue }) {
  return (
    <>
      <PriorityIcon priority={issue.priority} className="shrink-0" />
      <span className="w-14 shrink-0 text-xs text-muted-foreground">{issue.identifier}</span>
      <span className="min-w-0 flex-1 truncate">{issue.title}</span>
      {issue.assignee_type && issue.assignee_id && (
        <ActorAvatar actorType={issue.assignee_type} actorId={issue.assignee_id} size={20} enableHoverCard />
      )}
    </>
  );
}

// A QA cockpit lane — a titled, counted list of issue rows. Shared by the QA
// cockpit (verdict lanes) and the Bugs lens (bug lifecycle lanes) so both read
// as the same surface. Rows link wherever `href` points (qa review / issue).
export function Lane({
  icon: Icon,
  iconClass,
  title,
  subtitle,
  issues,
  href,
}: {
  icon: LucideIcon;
  iconClass: string;
  title: string;
  subtitle: string;
  issues: Issue[];
  href: (id: string) => string;
}) {
  return (
    <section className="rounded-lg border">
      <div className="flex items-center gap-2 border-b px-3 py-2">
        <Icon className={cn("size-4 shrink-0", iconClass)} />
        <span className="text-sm font-medium">{title}</span>
        <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">{issues.length}</span>
        <span className="ml-2 truncate text-[11px] text-muted-foreground">{subtitle}</span>
      </div>
      {issues.length === 0 ? (
        <p className="px-3 py-2 text-[12px] text-muted-foreground">Nothing here.</p>
      ) : (
        <ul className="divide-y">
          {issues.map((issue) => (
            <li key={issue.id}>
              <AppLink
                href={href(issue.id)}
                className="flex items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent/60"
              >
                <QAIssueRow issue={issue} />
              </AppLink>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
