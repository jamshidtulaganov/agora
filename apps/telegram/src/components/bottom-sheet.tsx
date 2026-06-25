import type { ReactNode } from "react";

// Minimal native-feeling bottom sheet. No component-library dependency so the
// Mini App keeps full control of the Telegram-style slide-up surface.
export function BottomSheet({
  open,
  onClose,
  title,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title?: string;
  children: ReactNode;
}) {
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex flex-col justify-end" role="dialog" aria-modal="true">
      <button
        type="button"
        aria-label="Close"
        className="absolute inset-0 bg-black/40"
        onClick={onClose}
      />
      <div className="relative max-h-[75vh] overflow-y-auto rounded-t-2xl border-t border-border bg-card pb-[max(env(safe-area-inset-bottom),0.5rem)]">
        <div className="sticky top-0 flex items-center justify-center bg-card pt-2">
          <span className="h-1 w-9 rounded-full bg-muted-foreground/30" />
        </div>
        {title && (
          <div className="px-4 pb-1 pt-2 text-sm font-semibold text-foreground">
            {title}
          </div>
        )}
        {children}
      </div>
    </div>
  );
}
