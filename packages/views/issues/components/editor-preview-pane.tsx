"use client";

import { useState, useEffect, useMemo } from "react";
import {
  Play,
  Square,
  Loader2,
  RotateCw,
  ExternalLink,
  TriangleAlert,
  Globe,
} from "lucide-react";
import { cn } from "@agora/ui/lib/utils";
import { proxyHeaders, absoluteBase } from "./editor-proxy-fetch";

// Live preview — the vibecoder's "Verify". Runs the project's dev server in the
// agent's worktree (daemon-side) and iframes it, so you see the app RUN, not the
// diff. The dev command is auto-detected from package.json (editable). Self-host
// only: talks to the daemon directly, like /editor/launch.

interface PreviewStatus {
  running: boolean;
  detected?: string;
  command?: string;
  url?: string;
  port?: number;
  /** Daemon health-listener route to this dev server ("/editor/local/<port>/").
   * In cloud mode the iframe rides `${daemonUrl}${proxy_path}` — the raw
   * 127.0.0.1 url is unreachable from the user's browser there. */
  proxy_path?: string;
}

interface PreviewStartResp {
  url?: string;
  port?: number;
  command?: string;
  running?: boolean;
  needs_command?: boolean;
  error?: string;
  warning?: string;
  log?: string;
  proxy_path?: string;
}

type PreviewState = "idle" | "starting" | "running" | "error";

export interface TestRunState {
  testState: "idle" | "running" | "done";
  testOut: string;
  testPassed: boolean | null;
  testCmd: string;
  parsedTests: {
    failed: { name: string; file: string }[];
    failedCount: number;
    passedCount: number;
  };
}

export function parseTestOutput(testOut: string): TestRunState["parsedTests"] {
  const failed: { name: string; file: string }[] = [];
  const failRe =
    /(?:^|\n)\s*(?:FAIL|×|✗)\s+(\S+\.(?:spec|test)\.[tj]sx?)\s*[>›]\s*([^\n]+)/g;
  let m: RegExpExecArray | null;
  while ((m = failRe.exec(testOut)) !== null) {
    failed.push({ file: m[1] ?? "", name: (m[2] ?? "").trim() });
  }
  const sum = testOut.match(/Tests\s+(\d+)\s+failed\s*\|\s*(\d+)\s+passed/);
  const passedSum = testOut.match(/Tests\s+(\d+)\s+passed/);
  return {
    failed,
    failedCount: sum ? Number(sum[1]) : failed.length,
    passedCount: sum ? Number(sum[2]) : passedSum ? Number(passedSum[1]) : 0,
  };
}

export function EditorPreviewPane({
  daemonUrl,
  workdir,
  defaultDevCommand,
  defaultTestCommand,
  onTestStart,
  testRunState,
  onTestResult,
}: {
  daemonUrl: string;
  workdir: string;
  /** Project-level dev command (settings.qa_smoke_cmd) — prefills the command
   *  input when no preview is running; the daemon's detection is the fallback. */
  defaultDevCommand?: string;
  /** Project-level test command (settings.qa_test_cmd) — sent to /editor/test;
   *  empty means the daemon auto-detects. */
  defaultTestCommand?: string;
  /** Called when the user clicks "Run tests" — lets the parent switch to the Tests tab. */
  onTestStart?: () => void;
  /** Lifted test state from the parent (editor-section). */
  testRunState?: TestRunState;
  /** Called with the result after a test run completes. */
  onTestResult?: (result: Omit<TestRunState, "parsedTests">) => void;
}) {
  const [command, setCommand] = useState("");
  const [url, setUrl] = useState<string | null>(null);
  const [state, setState] = useState<PreviewState>("idle");
  const [msg, setMsg] = useState("");
  const [log, setLog] = useState("");
  const [iframeKey, setIframeKey] = useState(0);
  // Where the running app is shown. "agora" iframes it in this pane; "local"
  // runs the same daemon dev server but the developer opens it in their own
  // browser (a real tab — better for apps that break inside an iframe: OAuth
  // redirects, frame-busting, strict CSP).
  const [mode, setMode] = useState<"agora" | "local">("agora");

  // Vite (and most dev servers) bind "localhost", which can resolve to ::1
  // (IPv6), but the daemon reports the URL as 127.0.0.1 (IPv4) — the iframe then
  // can't connect. Normalize to localhost so the browser reaches whatever the
  // dev server actually bound, on either stack.
  const toLocalhost = (u: string) => u.replace("://127.0.0.1:", "://localhost:");

  // Cloud: daemonUrl is a same-origin path base (/browser/proxy/<token>) and
  // the daemon's raw 127.0.0.1 url is unreachable — ride its proxy_path
  // through that base instead. Self-host keeps the direct localhost url.
  const resolveAppUrl = (r: { url?: string; proxy_path?: string }): string => {
    if (daemonUrl.startsWith("/") && r.proxy_path) return `${daemonUrl}${r.proxy_path}`;
    return r.url ? toLocalhost(r.url) : "";
  };

  // Local test state used only when parent hasn't lifted it (standalone usage).
  const [localTestState, setLocalTestState] = useState<"idle" | "running" | "done">("idle");
  const [localTestOut, setLocalTestOut] = useState("");
  const [localTestPassed, setLocalTestPassed] = useState<boolean | null>(null);
  const [localTestCmd, setLocalTestCmd] = useState("");

  const testState = testRunState?.testState ?? localTestState;
  const testOut = testRunState?.testOut ?? localTestOut;
  const testPassed = testRunState?.testPassed ?? localTestPassed;
  const testCmd = testRunState?.testCmd ?? localTestCmd;

  const [showRaw, setShowRaw] = useState(false);

  const parsedTests = useMemo(
    () => testRunState?.parsedTests ?? parseTestOutput(testOut),
    [testRunState, testOut],
  );

  const runTests = async () => {
    onTestStart?.();
    if (onTestResult) {
      onTestResult({ testState: "running", testOut: "", testPassed: null, testCmd: "" });
    } else {
      setLocalTestState("running");
      setLocalTestOut("");
      setLocalTestPassed(null);
    }
    try {
      const r = await fetch(`${absoluteBase(daemonUrl)}/editor/test`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        // Empty command still means "daemon auto-detects".
        body: JSON.stringify({ workdir, command: defaultTestCommand ?? "" }),
      });
      if (!r.ok) throw new Error(`could not run tests (${r.status})`);
      const d = (await r.json()) as {
        needs_command?: boolean;
        command?: string;
        passed?: boolean;
        output?: string;
      };
      if (d.needs_command) {
        const result = {
          testState: "done" as const,
          testOut:
            "No test command detected (package.json / Makefile / go.mod / composer.json) — nothing to run.",
          testPassed: null,
          testCmd: "",
        };
        if (onTestResult) onTestResult(result);
        else { setLocalTestState("done"); setLocalTestOut(result.testOut); setLocalTestCmd(""); }
        return;
      }
      const result = {
        testState: "done" as const,
        testOut: d.output || "",
        testPassed: d.passed === true,
        testCmd: d.command || "",
      };
      if (onTestResult) {
        onTestResult(result);
      } else {
        setLocalTestState("done");
        setLocalTestOut(result.testOut);
        setLocalTestPassed(result.testPassed);
        setLocalTestCmd(result.testCmd);
      }
    } catch (e) {
      const result = {
        testState: "done" as const,
        testOut: e instanceof Error ? e.message : "could not run tests",
        testPassed: false,
        testCmd: "",
      };
      if (onTestResult) onTestResult(result);
      else { setLocalTestState("done"); setLocalTestOut(result.testOut); setLocalTestPassed(false); }
    }
  };

  // On mount: sync with the daemon — reattach to a running preview, else
  // prefill the command input. Precedence: a running preview's actual command >
  // the project's configured dev command > the daemon's detection.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const r = await fetch(`${absoluteBase(daemonUrl)}/editor/preview/status`, {
          method: "POST",
          headers: proxyHeaders(daemonUrl),
          body: JSON.stringify({ workdir }),
        });
        if (!r.ok) return;
        const s = (await r.json()) as PreviewStatus;
        if (cancelled) return;
        setCommand(s.command || defaultDevCommand || s.detected || "");
        if (s.running && (s.url || s.proxy_path)) {
          setUrl(resolveAppUrl(s));
          setState("running");
        }
      } catch {
        /* daemon unreachable — stay idle */
      }
    })();
    return () => {
      cancelled = true;
    };
    // defaultDevCommand deliberately omitted: the project query resolving after
    // mount must not clobber a command the user already started editing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [daemonUrl, workdir]);

  // While running, keep syncing the bound port from the daemon. A slow first run
  // (deps re-optimize + port hunting past in-use ports) can return "could not
  // detect the port" up front — the iframe then points at the hint port, which
  // is dead. The daemon's /editor/preview/status re-scans the dev-server output
  // on every poll, so once it reports the real port we switch the iframe to it
  // and clear the warning, instead of staying stuck on the hint.
  useEffect(() => {
    if (state !== "running") return;
    let cancelled = false;
    const tick = async () => {
      try {
        const r = await fetch(`${absoluteBase(daemonUrl)}/editor/preview/status`, {
          method: "POST",
          headers: proxyHeaders(daemonUrl),
          body: JSON.stringify({ workdir }),
        });
        if (!r.ok) return;
        const s = (await r.json()) as PreviewStatus;
        if (cancelled || !s.running) return;
        const nextUrl = resolveAppUrl(s);
        if (nextUrl && nextUrl !== url) {
          setUrl(nextUrl);
          setIframeKey((k) => k + 1);
          setMsg("");
        }
      } catch {
        /* transient — keep the current url */
      }
    };
    const id = setInterval(() => void tick(), 3000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [state, daemonUrl, workdir, url]);

  const start = async (runMode: "agora" | "local") => {
    setMode(runMode);
    setState("starting");
    setMsg("");
    setLog("");
    try {
      const r = await fetch(`${absoluteBase(daemonUrl)}/editor/preview`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        body: JSON.stringify({ workdir, command: command.trim() }),
      });
      if (!r.ok) throw new Error(`preview failed (${r.status})`);
      const d = (await r.json()) as PreviewStartResp;
      if (d.needs_command) {
        setState("error");
        setMsg(
          "No dev command detected (package.json scripts, Makefile dev/start/run/serve, PHP index.php) — type one (e.g. npm run dev).",
        );
        return;
      }
      if (d.error) {
        setState("error");
        setMsg(d.error);
        setLog(d.log || "");
        return;
      }
      if (d.command) setCommand(d.command);
      if (d.url || d.proxy_path) {
        setUrl(resolveAppUrl(d));
        setState("running");
        setIframeKey((k) => k + 1);
        setMsg(d.warning || "");
        setLog(d.log || "");
        // Auto-run QA: tests fire as soon as the app is up, for either run mode.
        void runTests();
      } else {
        setState("error");
        setMsg("preview did not return a url");
      }
    } catch (e) {
      setState("error");
      setMsg(e instanceof Error ? e.message : "preview failed");
    }
  };

  const stop = async () => {
    try {
      await fetch(`${absoluteBase(daemonUrl)}/editor/preview/stop`, {
        method: "POST",
        headers: proxyHeaders(daemonUrl),
        body: JSON.stringify({ workdir }),
      });
    } catch {
      /* best effort */
    }
    setUrl(null);
    setState("idle");
    setMsg("");
  };

  const iconBtn =
    "rounded-md border border-border p-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground";

  return (
    <div className="flex min-w-0 flex-1 flex-col bg-background">
      <div className="flex shrink-0 items-center gap-1.5 border-b border-border px-2 py-1.5">
        <input
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          placeholder="dev command (e.g. npm run dev)"
          spellCheck={false}
          className="min-w-0 flex-1 rounded-md border border-border bg-background px-2 py-1 font-mono text-xs outline-none focus:border-primary/50"
        />
        {state === "running" ? (
          <>
            <button
              type="button"
              onClick={() => setIframeKey((k) => k + 1)}
              title="Reload preview"
              className={iconBtn}
            >
              <RotateCw className="h-3.5 w-3.5" />
            </button>
            {url && (
              <a
                href={url}
                target="_blank"
                rel="noreferrer noopener"
                title="Open in a new tab"
                className={iconBtn}
              >
                <ExternalLink className="h-3.5 w-3.5" />
              </a>
            )}
            <button
              type="button"
              onClick={() => void stop()}
              className="inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs text-muted-foreground transition-colors hover:border-destructive/50 hover:bg-destructive/10 hover:text-destructive"
            >
              <Square className="h-3 w-3" />
              Stop
            </button>
          </>
        ) : state === "starting" ? (
          <button
            type="button"
            disabled
            className="inline-flex items-center gap-1 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground opacity-50"
          >
            <Loader2 className="h-3 w-3 animate-spin" />
            Starting…
          </button>
        ) : (
          <>
            <button
              type="button"
              onClick={() => void start("agora")}
              title="Preview the app inside Agora (embedded)"
              className="inline-flex items-center gap-1 rounded-md bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90"
            >
              <Play className="h-3 w-3" />
              Run in Agora
            </button>
            <button
              type="button"
              onClick={() => void start("local")}
              title="Run the dev server and open it in your own browser"
              className="inline-flex items-center gap-1 rounded-md border border-border px-2.5 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <Globe className="h-3 w-3" />
              Run local
            </button>
          </>
        )}
      </div>

      <div className="relative min-h-0 flex-1">
        {state === "running" && url ? (
          mode === "local" ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 p-4 text-center text-xs text-muted-foreground">
              <Globe className="h-6 w-6" />
              <span>
                Dev server running on your machine — open it in your own browser.
              </span>
              <a
                href={url}
                target="_blank"
                rel="noreferrer noopener"
                className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90"
              >
                <ExternalLink className="h-3.5 w-3.5" />
                Open app in your browser
              </a>
              <span className="font-mono text-[10px] text-muted-foreground/70">
                {url}
              </span>
            </div>
          ) : (
            <iframe
              key={iframeKey}
              src={url}
              title="app preview"
              className="h-full w-full border-0 bg-white"
            />
          )
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-2 p-4 text-center text-xs text-muted-foreground">
            {state === "starting" ? (
              <>
                <Loader2 className="h-5 w-5 animate-spin" />
                <span>Starting the dev server…</span>
                <span className="text-[10px]">
                  first run may install deps — up to ~25s
                </span>
              </>
            ) : state === "error" ? (
              <>
                <TriangleAlert className="h-5 w-5 text-destructive" />
                <span className="max-w-[80%] text-destructive">{msg}</span>
              </>
            ) : (
              <>
                <Play className="h-5 w-5" />
                <span>Run the dev server to preview the app live.</span>
              </>
            )}
          </div>
        )}
        {msg && state === "running" && (
          <div className="absolute left-2 top-2 flex items-center gap-1 rounded bg-amber-500/90 px-2 py-1 text-[10px] text-white">
            <TriangleAlert className="h-3 w-3" />
            {msg}
          </div>
        )}
      </div>

      {(state === "running" || testState !== "idle") && (
        <div className="flex max-h-64 shrink-0 flex-col border-t border-border">
          <div className="flex items-center gap-2 px-2 py-1.5 text-xs">
            <span className="font-medium">Tests</span>
            {testCmd && (
              <span className="font-mono text-[10px] text-muted-foreground">
                {testCmd}
              </span>
            )}
            {testState === "running" && (
              <span className="inline-flex items-center gap-1 text-[10px] text-muted-foreground">
                <Loader2 className="h-3 w-3 animate-spin" />
                running tests…
              </span>
            )}
            {testState === "done" && (
              <span className="inline-flex items-center gap-2 text-[10px]">
                <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 font-medium text-emerald-600 dark:text-emerald-400">
                  ✓ {parsedTests.passedCount} passed
                </span>
                {parsedTests.failedCount > 0 && (
                  <span className="rounded bg-destructive/15 px-1.5 py-0.5 font-medium text-destructive">
                    ✗ {parsedTests.failedCount} failed
                  </span>
                )}
              </span>
            )}
            {testOut && (
              <button
                type="button"
                onClick={() => setShowRaw((v) => !v)}
                className="ml-auto text-[10px] text-muted-foreground underline-offset-2 hover:underline"
              >
                {showRaw ? "cases" : "raw"}
              </button>
            )}
            <button
              type="button"
              onClick={() => void runTests()}
              disabled={testState === "running"}
              title="Run the project's test suite"
              className={cn(
                "inline-flex items-center gap-1 rounded-md border border-border px-2 py-0.5 text-[10px] text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50",
                testOut ? "" : "ml-auto",
              )}
            >
              <Play className="h-2.5 w-2.5" />
              Run tests
            </button>
          </div>

          {/* Structured per-case view (default) vs raw terminal (toggle). */}
          {showRaw && testOut ? (
            <pre
              className={cn(
                "overflow-auto border-t border-border bg-black/90 p-2 font-mono text-[10px] leading-tight",
                testPassed === false ? "text-red-300" : "text-emerald-200",
              )}
            >
              {testOut}
            </pre>
          ) : (
            testState === "done" && (
              <div className="overflow-auto border-t border-border">
                {parsedTests.failed.length === 0 ? (
                  <div className="px-2 py-2 text-[11px] text-emerald-600 dark:text-emerald-400">
                    {/* Non-vitest runners (go test, phpunit) parse 0 cases —
                        report the exit code instead of "All 0 tests passed". */}
                    {testPassed
                      ? parsedTests.passedCount > 0
                        ? `All ${parsedTests.passedCount} tests passed ✓`
                        : "Tests passed (exit 0) ✓"
                      : (testOut.split("\n")[0] || "No tests found.")}
                  </div>
                ) : (
                  <ul className="divide-y divide-border/60">
                    {parsedTests.failed.map((t, i) => (
                      <li
                        key={`${t.file}-${i}`}
                        className="flex items-start gap-1.5 px-2 py-1 text-[10px]"
                      >
                        <span className="mt-px text-destructive">✗</span>
                        <span className="min-w-0">
                          <span className="text-foreground">{t.name}</span>
                          <span className="ml-1 font-mono text-muted-foreground/60">
                            {t.file.replace(/^.*\/tests?\//, "")}
                          </span>
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )
          )}
        </div>
      )}

      {log && (
        <pre
          className={cn(
            "max-h-24 shrink-0 overflow-auto border-t border-border bg-muted/40 p-2 text-[10px] leading-tight",
            state === "error" ? "text-destructive" : "text-muted-foreground",
          )}
        >
          {log}
        </pre>
      )}
    </div>
  );
}
