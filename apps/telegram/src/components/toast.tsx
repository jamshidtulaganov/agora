import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { CircleCheck, Info } from "lucide-react";

// App-level toast: dark pill sliding in from the top, auto-dismissing after
// 2.4s (design 5a). Tone "ok" = green check, "info" = blue info glyph.

type Tone = "ok" | "info";

interface ToastValue {
  toast: (message: string, tone?: Tone) => void;
}

const ToastContext = createContext<ToastValue | null>(null);

export function useToast(): ToastValue {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used within ToastProvider");
  return ctx;
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [current, setCurrent] = useState<{ message: string; tone: Tone; key: number } | null>(
    null,
  );
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const toast = useCallback((message: string, tone: Tone = "ok") => {
    if (timer.current) clearTimeout(timer.current);
    setCurrent({ message, tone, key: Date.now() });
    timer.current = setTimeout(() => setCurrent(null), 2400);
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      {current && (
        <div
          key={current.key}
          className="pointer-events-none absolute inset-x-4 top-[calc(env(safe-area-inset-top)+3rem)] z-50 flex animate-ag-toast-in items-center gap-2.5 rounded-xl bg-zinc-950 px-3.5 py-3 text-[13.5px] font-medium text-zinc-50 shadow-[0_8px_24px_rgba(9,9,11,0.25)]"
        >
          {current.tone === "ok" ? (
            <CircleCheck className="size-[19px] shrink-0 text-success" />
          ) : (
            <Info className="size-[19px] shrink-0 text-info" />
          )}
          <span className="min-w-0 flex-1 truncate">{current.message}</span>
        </div>
      )}
    </ToastContext.Provider>
  );
}
