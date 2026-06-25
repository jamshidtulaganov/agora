import type { ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@agora/ui/components/ui/button";

// Full-bleed centered status panel used for loading / empty / error states.
export function CenterMessage({
  title,
  subtitle,
  spinner,
  icon,
  actionLabel,
  onAction,
}: {
  title: string;
  subtitle?: string;
  spinner?: boolean;
  icon?: ReactNode;
  actionLabel?: string;
  onAction?: () => void;
}) {
  return (
    <div className="flex h-full flex-1 flex-col items-center justify-center gap-3 px-8 text-center">
      {spinner && <Loader2 className="size-6 animate-spin text-muted-foreground" />}
      {!spinner && icon && (
        <div className="flex size-14 items-center justify-center rounded-full bg-muted">
          {icon}
        </div>
      )}
      <div className="text-base font-medium text-foreground">{title}</div>
      {subtitle && (
        <div className="text-sm text-muted-foreground">{subtitle}</div>
      )}
      {actionLabel && onAction && (
        <Button variant="outline" size="sm" onClick={onAction} className="mt-2">
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
