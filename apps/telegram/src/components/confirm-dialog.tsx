import type { ReactNode } from "react";
import { cn } from "../lib/cn";

// Centered confirmation dialog (design 5a merge confirm): icon tile, title,
// body, brand primary button + text cancel. Backdrop tap cancels.

export function ConfirmDialog({
  open,
  icon,
  title,
  body,
  confirmLabel,
  cancelLabel,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  icon: ReactNode;
  title: string;
  body: string;
  confirmLabel: string;
  cancelLabel: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  if (!open) return null;
  return (
    <>
      <button
        type="button"
        aria-label={cancelLabel}
        onClick={onCancel}
        className="absolute inset-0 z-40 animate-ag-fade-in bg-black/40"
      />
      <div className="absolute inset-x-7 top-1/2 z-50 -translate-y-[60%] animate-ag-sheet-in rounded-3xl bg-card px-[22px] pb-4 pt-6 text-center shadow-[0_24px_64px_rgba(9,9,11,0.28)]">
        <span className="inline-flex size-[52px] items-center justify-center rounded-full bg-brand/10 text-brand">
          {icon}
        </span>
        <div className="mt-3 text-[17px] font-semibold tracking-[-0.2px] text-foreground">
          {title}
        </div>
        <p className="mt-1.5 text-[13.5px] leading-normal text-muted-foreground [text-wrap:pretty]">
          {body}
        </p>
        <div className="mt-[18px] flex flex-col gap-2">
          <button
            type="button"
            disabled={busy}
            onClick={onConfirm}
            className={cn(
              "rounded-xl bg-brand py-[13px] text-[15px] font-semibold text-brand-foreground transition-colors active:brightness-90",
              busy && "opacity-70",
            )}
          >
            {confirmLabel}
          </button>
          <button
            type="button"
            onClick={onCancel}
            className="py-[11px] text-[15px] font-medium text-muted-foreground"
          >
            {cancelLabel}
          </button>
        </div>
      </div>
    </>
  );
}
