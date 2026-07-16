import { autoUpdater, type UpdateDownloadedEvent } from "electron-updater";
import log from "electron-log";
import { app, type BrowserWindow, ipcMain } from "electron";

// Persist updater diagnostics to ~/Library/Logs/Agora/main.log (and the
// platform equivalents). Without a logger, electron-updater's default is
// `console`, whose output goes to a packaged app's stdout that nobody ever
// reads — a Squirrel.Mac rejection then leaves no trace anywhere on disk.
autoUpdater.logger = log;
log.transports.file.level = "info";

// Silent background updates: electron-updater downloads on its own as soon
// as `update-available` fires; we only surface UI when the package is fully
// downloaded and ready to install on next quit.
autoUpdater.autoDownload = true;
autoUpdater.autoInstallOnAppQuit = true;

// Windows arm64 ships its own update metadata channel because
// electron-builder's `latest.yml` is not arch-suffixed on Windows — both
// arches would otherwise collide on the same file in the GitHub Release.
// See scripts/package.mjs (builderArgsForTarget) for the publish-side half
// of this pact. Pin the channel here so arm64 clients fetch
// `latest-arm64.yml` instead of the x64 metadata.
if (process.platform === "win32" && process.arch === "arm64") {
  autoUpdater.channel = "latest-arm64";
}

const STARTUP_CHECK_DELAY_MS = 5_000;
const PERIODIC_CHECK_INTERVAL_MS = 60 * 60 * 1000; // 1 hour

// How long to wait for `quitAndInstall` to actually tear the app down before
// declaring the install a failure. On success the process is gone long before
// this fires; the timer only ever resolves on the failure path.
const INSTALL_GRACE_MS = 10_000;

export type ManualUpdateCheckResult =
  | {
      ok: true;
      currentVersion: string;
      latestVersion: string;
      available: boolean;
    }
  | { ok: false; error: string };

// `quitAndInstall` only ever reports failure: a successful install never
// returns, because the process is replaced.
export type InstallUpdateResult = { ok: false; error: string };

type RendererChannel =
  | "updater:update-available"
  | "updater:download-progress"
  | "updater:update-downloaded";

function isDestroyedObjectError(err: unknown): boolean {
  return err instanceof Error && err.message.includes("Object has been destroyed");
}

function sendToLiveRenderer(
  win: BrowserWindow | null,
  channel: RendererChannel,
  payload: unknown,
): void {
  if (!win || win.isDestroyed()) return;

  try {
    const { webContents } = win;
    if (webContents.isDestroyed()) return;
    webContents.send(channel, payload);
  } catch (err) {
    if (isDestroyedObjectError(err)) return;
    throw err;
  }
}

// Single-flight guard around checkForUpdates(). With autoDownload=true the
// startup, periodic, and manual triggers can all kick off downloads, and
// overlapping calls have caused duplicate download warnings in the past
// (see electronjs.org/docs/latest/api/auto-updater). Coalesce concurrent
// callers onto the same in-flight promise.
let inFlightCheck: Promise<unknown> | null = null;
function checkForUpdatesOnce(): Promise<unknown> {
  if (inFlightCheck) return inFlightCheck;
  const p = autoUpdater
    .checkForUpdates()
    .then((result) => {
      // checkForUpdates resolves as soon as metadata is fetched; the actual
      // download (when autoDownload=true) is exposed on result.downloadPromise.
      // Without a handler a download failure becomes an unhandled rejection
      // in the main process — Node may terminate it on future versions.
      void (result as { downloadPromise?: Promise<unknown> } | null)?.downloadPromise?.catch(
        (err) => {
          console.error("Failed to download update:", err);
        },
      );
      return result;
    })
    .finally(() => {
      if (inFlightCheck === p) inFlightCheck = null;
    });
  inFlightCheck = p;
  return p;
}

export function setupAutoUpdater(getMainWindow: () => BrowserWindow | null): void {
  // Retained so a failed install can explain *why* rather than reporting a
  // bare timeout. electron-updater surfaces Squirrel.Mac's rejection through
  // the `error` event, long before the user ever clicks "Restart now".
  let lastUpdaterError: Error | null = null;

  autoUpdater.on("update-available", (info) => {
    // Forwarded for renderer-side state tracking only; the notification UI
    // does not render an "available" affordance with autoDownload=true.
    sendToLiveRenderer(getMainWindow(), "updater:update-available", {
      version: info.version,
      releaseNotes: info.releaseNotes,
    });
  });

  autoUpdater.on("download-progress", (progress) => {
    sendToLiveRenderer(getMainWindow(), "updater:download-progress", {
      percent: progress.percent,
    });
  });

  autoUpdater.on("update-downloaded", (info: UpdateDownloadedEvent) => {
    sendToLiveRenderer(getMainWindow(), "updater:update-downloaded", {
      version: info.version,
      releaseNotes: info.releaseNotes,
    });
  });

  autoUpdater.on("error", (err) => {
    lastUpdaterError = err instanceof Error ? err : new Error(String(err));
    log.error("Auto-updater error:", err);
  });

  // Retained for IPC back-compat with older renderer bundles. With
  // autoDownload=true the renderer no longer triggers this path.
  ipcMain.handle("updater:download", () => {
    return autoUpdater.downloadUpdate();
  });

  // `autoUpdater.quitAndInstall()` is not a promise and does not throw when it
  // cannot install. On macOS it hands off to Squirrel.Mac, which validates the
  // update against the running app's designated requirement; on rejection
  // electron-updater's MacUpdater.quitAndInstall() simply parks an
  // `update-downloaded` listener that never fires and returns normally. The
  // click then does nothing, forever, with no error anywhere — so treat "still
  // running after the grace period" as the failure signal.
  ipcMain.handle("updater:install", async (): Promise<InstallUpdateResult> => {
    try {
      autoUpdater.quitAndInstall(false, true);
    } catch (err) {
      const error = err instanceof Error ? err.message : String(err);
      log.error("Failed to install update:", err);
      return { ok: false, error };
    }

    await new Promise((resolve) => setTimeout(resolve, INSTALL_GRACE_MS));

    const detail =
      lastUpdaterError?.message ??
      "the installer did not start and reported no error";
    log.error(`Update install did not restart the app: ${detail}`);
    return {
      ok: false,
      error: `Couldn't apply the update automatically: ${detail}`,
    };
  });

  ipcMain.handle("updater:check", async (): Promise<ManualUpdateCheckResult> => {
    try {
      const result = (await checkForUpdatesOnce()) as
        | { updateInfo: { version: string }; isUpdateAvailable?: boolean }
        | null;
      const currentVersion = app.getVersion();
      // Trust electron-updater's own decision rather than re-deriving it from
      // a version-string compare. The two diverge for pre-release channels,
      // staged rollouts, downgrades, and minimum-system-version gates — in
      // those cases updateInfo.version differs from app.getVersion() but no
      // `update-available` event fires, so showing "available" here would
      // promise a download prompt that never appears.
      return {
        ok: true,
        currentVersion,
        latestVersion: result?.updateInfo.version ?? currentVersion,
        available: result?.isUpdateAvailable ?? false,
      };
    } catch (err) {
      return {
        ok: false,
        error: err instanceof Error ? err.message : String(err),
      };
    }
  });

  // Initial check shortly after startup so we don't block boot.
  setTimeout(() => {
    checkForUpdatesOnce().catch((err) => {
      console.error("Failed to check for updates:", err);
    });
  }, STARTUP_CHECK_DELAY_MS);

  // Background poll so long-running sessions still pick up new releases
  // without requiring the user to restart the app.
  setInterval(() => {
    checkForUpdatesOnce().catch((err) => {
      console.error("Periodic update check failed:", err);
    });
  }, PERIODIC_CHECK_INTERVAL_MS);
}
