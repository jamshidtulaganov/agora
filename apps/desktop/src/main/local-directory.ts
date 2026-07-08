import { ipcMain, dialog, BrowserWindow } from "electron";
import { access, mkdir, readFile, rename, stat, writeFile } from "fs/promises";
import { constants as fsConstants } from "fs";
import { homedir } from "os";
import { basename, isAbsolute, join, normalize, sep } from "path";

export interface PickDirectoryResult {
  ok: boolean;
  path?: string;
  basename?: string;
  /** Set when ok=false. "cancelled" = user dismissed; otherwise an error blurb. */
  reason?: "cancelled" | "no_window" | "error";
  error?: string;
}

export interface ValidateLocalDirectoryResult {
  ok: boolean;
  /** When ok=false, identifies which check failed so the renderer can render a
   *  specific message without parsing free-form text. */
  reason?:
    | "not_absolute"
    | "not_found"
    | "not_a_directory"
    | "not_readable"
    | "not_writable"
    | "error";
  error?: string;
}

async function validateLocalDirectory(
  path: string,
): Promise<ValidateLocalDirectoryResult> {
  if (!path || !isAbsolute(path)) {
    return { ok: false, reason: "not_absolute" };
  }
  try {
    const st = await stat(path);
    if (!st.isDirectory()) return { ok: false, reason: "not_a_directory" };
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code;
    if (code === "ENOENT") return { ok: false, reason: "not_found" };
    return { ok: false, reason: "error", error: errorMessage(err) };
  }
  try {
    await access(path, fsConstants.R_OK);
  } catch {
    return { ok: false, reason: "not_readable" };
  }
  try {
    await access(path, fsConstants.W_OK);
  } catch {
    return { ok: false, reason: "not_writable" };
  }
  return { ok: true };
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

export interface ApproveLocalDirectoryResult {
  ok: boolean;
  /** "protected" = path the daemon would refuse anyway (home, dot-dirs). */
  reason?: "not_absolute" | "protected" | "error";
  error?: string;
}

/**
 * Records the machine owner's consent for local_directory agent execution in
 * ~/.agora/local-dirs.json — the same file the daemon re-reads on every task
 * and `agora daemon allow-dir` writes. Picking a folder in the desktop app IS
 * the consent gesture, so the attach flow calls this right after validation.
 *
 * Only a minimal protected-path check lives here (home itself, dot-dirs
 * directly under home, ~/Library, AppData): the daemon's denylist is the
 * authoritative enforcement at task time, and a full TS mirror would drift.
 */
async function approveLocalDirectory(
  path: string,
  // Injectable so tests never touch the real ~/.agora — a polluted real
  // allowlist would silently grant agent access outside the test sandbox.
  homeDir: string = homedir(),
): Promise<ApproveLocalDirectoryResult> {
  if (!path || !isAbsolute(path)) return { ok: false, reason: "not_absolute" };
  const cleaned = normalize(path).replace(/[\\/]+$/, "");
  const home = normalize(homeDir).replace(/[\\/]+$/, "");
  if (cleaned === home) return { ok: false, reason: "protected" };
  if (cleaned.startsWith(home + sep)) {
    const first = cleaned.slice(home.length + 1).split(sep)[0] ?? "";
    if (
      first.startsWith(".") ||
      (process.platform === "darwin" && first === "Library") ||
      (process.platform === "win32" && first.toLowerCase() === "appdata")
    ) {
      return { ok: false, reason: "protected" };
    }
  }

  try {
    const dir = join(home, ".agora");
    const file = join(dir, "local-dirs.json");
    let dirs: string[] = [];
    try {
      const parsed = JSON.parse(await readFile(file, "utf8")) as {
        dirs?: unknown;
      };
      if (Array.isArray(parsed.dirs)) {
        dirs = parsed.dirs.filter((d): d is string => typeof d === "string");
      }
    } catch {
      // Missing or corrupt file: approve-on-pick is also the repair path —
      // rewrite a fresh file rather than failing the attach flow.
    }
    if (!dirs.includes(cleaned)) dirs.push(cleaned);
    dirs = [...new Set(dirs)].sort();
    await mkdir(dir, { recursive: true, mode: 0o700 });
    // Atomic write (temp + rename): the daemon and the agora CLI read/write
    // the same file, and a torn write must never eat existing approvals.
    const tmp = join(dir, `.local-dirs-${process.pid}-${Date.now()}.tmp`);
    await writeFile(
      tmp,
      JSON.stringify({ version: 1, dirs }, null, 2) + "\n",
      { mode: 0o600 },
    );
    await rename(tmp, file);
    return { ok: true };
  } catch (err) {
    return { ok: false, reason: "error", error: errorMessage(err) };
  }
}

export function setupLocalDirectory(
  windowGetter: () => BrowserWindow | null,
): void {
  ipcMain.handle(
    "local-directory:pick",
    async (_event, defaultPath?: string): Promise<PickDirectoryResult> => {
      const win = windowGetter();
      if (!win) return { ok: false, reason: "no_window" };
      try {
        const result = await dialog.showOpenDialog(win, {
          // Multiple-selection is intentionally disabled — a project_resource
          // points at a single directory, and the create flow expects one
          // path per click. Multi-add would have to be a separate UX.
          properties: ["openDirectory", "createDirectory"],
          ...(defaultPath ? { defaultPath } : {}),
        });
        if (result.canceled || result.filePaths.length === 0) {
          return { ok: false, reason: "cancelled" };
        }
        const picked = result.filePaths[0];
        if (!picked) return { ok: false, reason: "cancelled" };
        return { ok: true, path: picked, basename: basename(picked) };
      } catch (err) {
        return { ok: false, reason: "error", error: errorMessage(err) };
      }
    },
  );

  ipcMain.handle(
    "local-directory:validate",
    (_event, path: string): Promise<ValidateLocalDirectoryResult> =>
      validateLocalDirectory(path),
  );

  ipcMain.handle(
    "local-directory:approve",
    (_event, path: string): Promise<ApproveLocalDirectoryResult> =>
      approveLocalDirectory(path),
  );
}

// Exported for tests only — the IPC wiring above is the real surface.
export { approveLocalDirectory as approveLocalDirectoryForTest };
