"use client";

import { Bot, BriefcaseBusiness, CalendarCheck, ListChecks, type LucideIcon } from "lucide-react";
import { cn } from "@agora/ui/lib/utils";

// Templates name their icon as a lucide component name. Only the icons the
// setup's templates use are bundled; anything else falls back to Bot rather
// than pulling the whole icon set into the app.
const ICONS: Record<string, LucideIcon> = {
  BriefcaseBusiness,
  CalendarCheck,
  ListChecks,
};

// Static class map so Tailwind sees every variant. Unknown accents (a newer
// template) get the neutral style.
const ACCENTS: Record<string, string> = {
  info: "bg-info/10 text-info",
  success: "bg-success/10 text-success",
  warning: "bg-warning/10 text-warning",
  primary: "bg-primary/10 text-primary",
  secondary: "bg-secondary text-secondary-foreground",
};

export function TemplateIcon({
  icon,
  accent,
  className,
}: {
  icon?: string;
  accent?: string;
  className?: string;
}) {
  const Icon = (icon && ICONS[icon]) || Bot;
  const tone = (accent && ACCENTS[accent]) || "bg-muted text-muted-foreground";
  return (
    <span className={cn("flex size-8 shrink-0 items-center justify-center rounded-md", tone, className)}>
      <Icon className="size-4" aria-hidden />
    </span>
  );
}
