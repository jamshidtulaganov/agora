import { useCallback, useEffect, useState } from "react";
import { AlertCircle, Loader2, RefreshCw, X } from "lucide-react";

const RELEASES_URL = "https://github.com/jamshidtulaganov/agora-cli/releases/latest";

// Downloads run silently in the background (main process has
// autoDownload=true). The renderer only renders UI once the package is fully
// downloaded and waiting for a restart.
//
// "ready" is not a promise that the install will succeed: on macOS
// electron-updater emits `update-downloaded` once the zip is on disk, before
// Squirrel.Mac has accepted it. A rejection only surfaces when the user clicks
// through and `installUpdate()` comes back with an error, so the failed state
// has to be a first-class part of this component.
type UpdateState =
  | { status: "idle" }
  | { status: "ready"; version: string }
  | { status: "installing"; version: string }
  | { status: "failed"; version: string; error: string };

export function UpdateNotification() {
  const [state, setState] = useState<UpdateState>({ status: "idle" });
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    const cleanup = window.updater.onUpdateDownloaded((info) => {
      setState({ status: "ready", version: info.version });
      setDismissed(false);
    });
    return cleanup;
  }, []);

  const handleInstall = useCallback(async () => {
    if (state.status !== "ready") return;
    const { version } = state;
    setState({ status: "installing", version });
    // Resolves only if the install failed — on success the process is gone.
    const result = await window.updater.installUpdate();
    setState({ status: "failed", version, error: result.error });
  }, [state]);

  if (state.status === "idle") return null;
  if (dismissed) return null;

  const failed = state.status === "failed";

  return (
    <div className="fixed bottom-4 right-4 z-50 w-80 rounded-lg border border-border bg-background p-4 shadow-lg animate-in slide-in-from-bottom-2 fade-in duration-300">
      <button
        type="button"
        onClick={() => setDismissed(true)}
        className="absolute top-2 right-2 rounded-md p-1 text-muted-foreground hover:text-foreground transition-colors"
      >
        <X className="size-3.5" />
      </button>

      <div className="flex items-start gap-3">
        <div
          className={`mt-0.5 rounded-md p-1.5 ${failed ? "bg-destructive/10" : "bg-success/10"}`}
        >
          {failed ? (
            <AlertCircle className="size-4 text-destructive" />
          ) : (
            <RefreshCw className="size-4 text-success" />
          )}
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium">
            {failed ? "Update failed" : "Update ready"}
          </p>
          <p className="text-xs text-muted-foreground mt-0.5">
            {failed
              ? state.error
              : `v${state.version} will be applied on next launch.`}
          </p>
          <div className="mt-2 flex items-center gap-1.5">
            {failed ? (
              <button
                type="button"
                onClick={() => window.desktopAPI.openExternal(RELEASES_URL)}
                className="inline-flex items-center rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-foreground hover:bg-accent transition-colors"
              >
                Download manually
              </button>
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => setDismissed(true)}
                  className="inline-flex items-center rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-foreground hover:bg-accent transition-colors"
                >
                  Later
                </button>
                <button
                  type="button"
                  onClick={handleInstall}
                  disabled={state.status === "installing"}
                  className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 transition-colors disabled:opacity-60"
                >
                  {state.status === "installing" && (
                    <Loader2 className="size-3 animate-spin" />
                  )}
                  {state.status === "installing" ? "Restarting…" : "Restart now"}
                </button>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
