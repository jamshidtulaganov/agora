import { describe, it, expect, vi, beforeEach } from "vitest";
import { mkdtemp, readFile, mkdir, writeFile } from "fs/promises";
import { tmpdir } from "os";
import { join, sep } from "path";

// The SUT imports electron for the IPC wiring; the approve logic under test
// is pure fs. Stub the electron surface so the module loads in plain node.
vi.mock("electron", () => ({
  ipcMain: { handle: vi.fn() },
  dialog: { showOpenDialog: vi.fn() },
  BrowserWindow: class {},
}));

import { approveLocalDirectoryForTest } from "./local-directory";

// Explicit home injection: mocking node builtins is unreliable under
// electron-vite/vitest and a miss writes to the REAL ~/.agora allowlist.
const homeRef = { current: "" };
const approve = (path: string) =>
  approveLocalDirectoryForTest(path, homeRef.current);

const allowlistPath = () => join(homeRef.current, ".agora", "local-dirs.json");

async function readAllowlist(): Promise<{ version: number; dirs: string[] }> {
  return JSON.parse(await readFile(allowlistPath(), "utf8")) as {
    version: number;
    dirs: string[];
  };
}

describe("approveLocalDirectory", () => {
  beforeEach(async () => {
    homeRef.current = await mkdtemp(join(tmpdir(), "agora-home-"));
  });

  it("records an approval in ~/.agora/local-dirs.json", async () => {
    const proj = join(homeRef.current, "projects", "app");
    await mkdir(proj, { recursive: true });
    const res = await approve(proj);
    expect(res).toEqual({ ok: true });
    const file = await readAllowlist();
    expect(file.version).toBe(1);
    expect(file.dirs).toEqual([proj]);
  });

  it("dedupes repeat approvals and keeps the file sorted", async () => {
    const a = join(homeRef.current, "projects", "a");
    const b = join(homeRef.current, "projects", "b");
    await mkdir(a, { recursive: true });
    await mkdir(b, { recursive: true });
    await approve(b);
    await approve(a);
    await approve(b);
    const file = await readAllowlist();
    expect(file.dirs).toEqual([a, b]);
  });

  it("rejects relative paths", async () => {
    const res = await approve("relative/path");
    expect(res.ok).toBe(false);
    expect(res.reason).toBe("not_absolute");
  });

  it("rejects $HOME itself and protected home subtrees", async () => {
    for (const p of [
      homeRef.current,
      join(homeRef.current, ".ssh"),
      join(homeRef.current, ".aws", "credentials-dir"),
    ]) {
      const res = await approve(p);
      expect(res.ok, p).toBe(false);
      expect(res.reason, p).toBe("protected");
    }
    // A dot-dir NOT directly under home is a legal project location.
    const nested = join(homeRef.current, "projects", ".hidden");
    await mkdir(nested, { recursive: true });
    expect((await approve(nested)).ok).toBe(true);
  });

  it("does not approve a sibling sharing a dot-prefix boundary", async () => {
    // ~/.ssh-notes is odd but not ~/.ssh — still starts with "." though, so
    // it IS a dot-dir directly under home and stays protected.
    const res = await approve(join(homeRef.current, ".ssh-notes"));
    expect(res.reason).toBe("protected");
  });

  it("repairs a corrupt allowlist file instead of failing", async () => {
    const dir = join(homeRef.current, ".agora");
    await mkdir(dir, { recursive: true });
    await writeFile(join(dir, "local-dirs.json"), "{corrupt", "utf8");
    const proj = join(homeRef.current, "code");
    await mkdir(proj, { recursive: true });
    const res = await approve(proj);
    expect(res.ok).toBe(true);
    const file = await readAllowlist();
    expect(file.dirs).toEqual([proj]);
  });

  it("normalizes trailing separators", async () => {
    const proj = join(homeRef.current, "code");
    await mkdir(proj, { recursive: true });
    await approve(proj + sep);
    const file = await readAllowlist();
    expect(file.dirs).toEqual([proj]);
  });
});
