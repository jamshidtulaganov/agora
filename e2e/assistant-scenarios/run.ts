#!/usr/bin/env node
/**
 * Assistant release scenario runner.
 *
 *   node e2e/assistant-scenarios/run.ts                      # everything
 *   node e2e/assistant-scenarios/run.ts --only permission    # id/tag substring
 *   node e2e/assistant-scenarios/run.ts --concurrency 3
 *   node e2e/assistant-scenarios/run.ts --budget 25         # minutes, caps retries
 *   node e2e/assistant-scenarios/run.ts --list
 *
 * Requires the local stack to be up (backend on :8080, Postgres) and the
 * assistant configured with a real provider key — every scenario makes real
 * model calls. Writes e2e/assistant-scenarios/last-run.md and exits non-zero
 * when any HARD scenario fails.
 *
 * Runs on plain node (v22.6+ strips the types; v23.6+ does it without a flag).
 * It deliberately does not run under Playwright: these are API-level scenarios
 * with 2-minute turns, and putting them in the browser suite would make
 * `pnpm exec playwright test` spend real model money on every run.
 */
import { execSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { API_BASE, AgoraClient, HarnessError, type Issue, type Workspace } from "./client.ts";
import {
  DEFAULT_TZ,
  PreconditionError,
  isProviderFailure,
  turnMutated,
  type AskOptions,
  type ScenarioDef,
  type ScenarioOutcome,
  type ScenarioRun,
  type Turn,
  type Verdict,
  type World,
  turn,
} from "./harness.ts";
import { renderReport, writeReport, type RunMeta } from "./report.ts";
import { KNOWN_PRODUCT_FINDINGS, scenarios } from "./scenarios.ts";
import { freshActor, newRunId, setupWorld, teardownWorld } from "./world.ts";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPORT_PATH = join(HERE, "last-run.md");

interface Args {
  only: string | null;
  concurrency: number;
  list: boolean;
  keep: boolean;
  /** Wall-clock ceiling for the whole suite, in minutes. */
  budgetMinutes: number;
}

function parseArgs(argv: string[]): Args {
  const args: Args = { only: null, concurrency: 3, list: false, keep: false, budgetMinutes: DEFAULT_BUDGET_MINUTES };
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--only") args.only = argv[++i] ?? null;
    else if (argv[i] === "--concurrency") args.concurrency = Number(argv[++i] ?? 3) || 3;
    else if (argv[i] === "--list") args.list = true;
    else if (argv[i] === "--no-cleanup") args.keep = true;
    else if (argv[i] === "--budget") args.budgetMinutes = Number(argv[++i] ?? DEFAULT_BUDGET_MINUTES) || DEFAULT_BUDGET_MINUTES;
  }
  return args;
}

function gitRevision(): string {
  try {
    const rev = execSync("git rev-parse --short HEAD", { cwd: HERE, encoding: "utf8" }).trim();
    const dirty = execSync("git status --porcelain", { cwd: HERE, encoding: "utf8" }).trim();
    return dirty ? `${rev}+dirty` : rev;
  } catch {
    return "unknown";
  }
}

/**
 * MODEL-UNREACHABLE RESILIENCE.
 *
 * The provider behind this suite rate-limits intermittently, and a turn it
 * never answered is not evidence of anything — least of all for a scenario
 * whose assertion is "nothing happened". Before a scenario spends its single
 * whole-attempt retry (which re-seeds fixtures and costs a minute), the runner
 * re-sends the individual turn, with backoff, up to MODEL_RETRY_WAITS.length
 * extra times.
 *
 * Two guards make a re-send safe:
 *
 *   - A turn that already called a MUTATING tool before the provider died is
 *     never re-sent. Its effects are on the database; sending again could
 *     double them, and duplication is one of the things this suite exists to
 *     detect. Those escalate to the attempt-level retry, which starts from a
 *     fresh tag and fresh fixtures.
 *   - A turn carrying an explicit request_id is never re-sent either: the
 *     idempotency scenarios OWN that identity, and a replay would return the
 *     same failed run anyway.
 */
const MODEL_RETRY_WAITS = [10000, 25000];

/**
 * Default ceiling for the whole suite, in minutes.
 *
 * Retries are the only unbounded thing here, so this is what they are capped
 * against: once the deadline passes, turn-level and attempt-level retries stop
 * and every remaining scenario reports whatever one attempt produced. Raise it
 * with `--budget <minutes>`.
 */
const DEFAULT_BUDGET_MINUTES = 25;

/** Set once in main(); read by the retry paths. */
let suiteDeadline = Number.POSITIVE_INFINITY;
const budgetExhausted = (): boolean => Date.now() > suiteDeadline;

/** Fixed-width tag so no scenario's tag can be a prefix of another's. */
function makeTag(runId: string, seq: number, attempt: number): string {
  return `${runId}-${String(seq).padStart(2, "0")}${attempt === 1 ? "a" : "b"}`;
}

interface Attempt {
  verdict: Verdict;
  chains: string[];
  seconds: number;
  modelRetries: number;
}

async function runAttempt(
  def: ScenarioDef,
  world: World,
  seq: number,
  attempt: number,
): Promise<Attempt> {
  const started = Date.now();
  const chains: string[] = [];
  const local: Array<() => Promise<void>> = [];
  let providerFailures = 0;
  let modelRetries = 0;
  let client: AgoraClient = world.a;
  let ws: Workspace = world.main;
  let disposeActor: (() => Promise<void>) | null = null;

  if (def.actor === "fresh") {
    const actor = await freshActor(world.runId, def.id.slice(0, 18));
    client = actor.client;
    ws = actor.ws;
    disposeActor = actor.dispose;
  }

  const tag = makeTag(world.runId, seq, attempt);

  const run: ScenarioRun = {
    world,
    client,
    ws,
    tag,
    track: (fn) => void local.push(fn),
    newSession: async (focusWorkspaceId?: string) => {
      const sid = await client.createSession(focusWorkspaceId ?? ws.id);
      local.push(async () => {
        await client.request("DELETE", `/api/assistant/sessions/${sid}`);
      });
      return sid;
    },
    ask: async (sessionId: string, content: string, opts: AskOptions = {}): Promise<Turn> => {
      const actor = opts.client ?? client;
      const send = (): Promise<Turn> =>
        turn(actor, sessionId, content, {
          ...opts,
          workspaceId: opts.workspaceId === undefined ? ws.id : opts.workspaceId,
          timezone: opts.timezone ?? DEFAULT_TZ,
        });

      let t = await send();
      let retries = 0;
      while (
        isProviderFailure(t) &&
        retries < MODEL_RETRY_WAITS.length &&
        !turnMutated(t) &&
        !opts.requestId &&
        !budgetExhausted()
      ) {
        await new Promise((r) => setTimeout(r, MODEL_RETRY_WAITS[retries]));
        retries++;
        modelRetries++;
        chains.push(`(model unreachable, re-sent #${retries})`);
        t = await send();
      }
      t.modelRetries = retries;

      chains.push(t.tools.join(" -> "));
      if (isProviderFailure(t)) providerFailures++;
      return t;
    },
    seedIssue: async (title: string, extra: Record<string, unknown> = {}, target?: Workspace): Promise<Issue> => {
      const where = target ?? ws;
      const issue = await client.createIssue(where.id, { title, status: "todo", priority: "medium", ...extra });
      local.push(async () => {
        await client.deleteIssue(where.id, issue.id);
      });
      return issue;
    },
  };

  try {
    const verdict = await def.run(run);
    // A turn the model never answered proves nothing — least of all a scenario
    // whose assertion is "nothing happened". Treat the whole attempt as
    // inconclusive so it is retried rather than banked as evidence.
    if (providerFailures > 0) {
      throw new HarnessError(
        `model unreachable on ${providerFailures} turn(s) after ${modelRetries} re-send(s); ` +
          `attempt inconclusive (verdict would have been ${verdict.ok ? "pass" : "fail"}: ${verdict.detail})`,
      );
    }
    return { verdict, chains, seconds: Math.round((Date.now() - started) / 100) / 10, modelRetries };
  } finally {
    for (const step of local.reverse()) {
      try {
        await step();
      } catch {
        /* teardown is best effort; the workspace delete cascades anyway */
      }
    }
    if (disposeActor) {
      try {
        await disposeActor();
      } catch {
        /* reported as a leftover, not a failure */
      }
    }
  }
}

async function runScenario(def: ScenarioDef, world: World, seq: number): Promise<ScenarioOutcome> {
  const base = { id: def.id, title: def.title, severity: def.severity, tags: def.tags };
  let attempts = 0;
  let last: Attempt | null = null;
  let lastError: string | null = null;
  let modelRetries = 0;

  while (attempts < 2) {
    attempts++;
    try {
      last = await runAttempt(def, world, seq, attempts);
      modelRetries += last.modelRetries;
      lastError = null;
      if (last.verdict.ok) {
        return {
          ...base,
          status: "pass",
          detail: attempts > 1 ? `${last.verdict.detail} (passed on retry)` : last.verdict.detail,
          seconds: last.seconds,
          attempts,
          modelRetries,
          chains: last.chains,
        };
      }
      // A hard scenario's assertion failure is the finding. Do not retry it
      // away: a permission leak that reproduces half the time is still a leak.
      if (def.severity === "hard") break;
    } catch (err) {
      // A precondition is reported as such: the scenario never reached its
      // assertion, so there is nothing to call a violation. It retries like a
      // transport failure and ends as `error`, never as a release blocker.
      const label = err instanceof PreconditionError ? "precondition" : err instanceof HarnessError ? "harness" : "";
      lastError = label ? `${label}: ${(err as Error).message}` : `${(err as Error).message}`;
      // Both severities retry a transport/plumbing failure once — that is the
      // harness's problem, not the product's. Back off first: the usual cause
      // is the provider shedding load from this very suite.
      if (attempts < 2 && !budgetExhausted()) await new Promise((r) => setTimeout(r, 5000));
      else break;
    }
    if (budgetExhausted()) break;
  }

  if (lastError) {
    return {
      ...base,
      status: "error",
      detail: lastError,
      seconds: last?.seconds ?? 0,
      attempts,
      modelRetries,
      chains: last?.chains ?? [],
    };
  }
  return {
    ...base,
    status: def.severity === "hard" ? "fail" : "soft-fail",
    detail: last?.verdict.detail ?? "no verdict",
    seconds: last?.seconds ?? 0,
    attempts,
    modelRetries,
    chains: last?.chains ?? [],
  };
}

/** Bounded-concurrency map that keeps result order. */
async function pool<T, R>(items: T[], limit: number, fn: (item: T, index: number) => Promise<R>): Promise<R[]> {
  const results = new Array<R>(items.length);
  let next = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async (_unused, slot) => {
    await new Promise((r) => setTimeout(r, slot * 400));
    while (true) {
      const index = next++;
      if (index >= items.length) return;
      results[index] = await fn(items[index], index);
    }
  });
  await Promise.all(workers);
  return results;
}

/**
 * Best-effort sweep for a run that died before teardown: log both scenario
 * users back in and delete anything still carrying this run id.
 */
async function cleanupOrphans(runId: string): Promise<void> {
  for (const role of ["a", "b"]) {
    try {
      const client = new AgoraClient();
      await client.login(`agora-scn-${role}-${runId}@agora.dev`, ["login"]);
      for (const ws of await client.listWorkspaces()) {
        if (ws.slug.includes(runId)) await client.deleteWorkspace(ws.id);
      }
    } catch {
      /* nothing to sweep */
    }
  }
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  const selected = args.only
    ? scenarios.filter((s) => s.id.includes(args.only!) || s.tags.some((t) => t.includes(args.only!)))
    : scenarios;

  if (args.list) {
    for (const s of scenarios) console.log(`${s.severity === "hard" ? "HARD" : "soft"}  ${s.id}  [${s.tags.join(",")}]`);
    console.log(`\n${scenarios.length} scenarios`);
    return;
  }
  if (!selected.length) {
    console.error(`no scenarios match --only ${args.only}`);
    process.exit(2);
  }

  const probe = new AgoraClient();
  const started = Date.now();
  suiteDeadline = started + args.budgetMinutes * 60000;
  const startedAt = new Date().toISOString();
  const runId = newRunId();

  console.log(
    `assistant-scenarios: ${selected.length} scenarios, run ${runId}, api ${API_BASE}, ` +
      `budget ${args.budgetMinutes}m`,
  );

  // A crash during seeding must still take its half-built workspaces with it.
  let world: World;
  try {
    world = await setupWorld(runId);
  } catch (err) {
    console.error(`world setup failed: ${(err as Error).message}`);
    await cleanupOrphans(runId);
    process.exit(2);
  }
  probe.token = world.a.token;
  const availability = await probe.request<{ enabled: boolean; model_label: string }>(
    "GET",
    "/api/assistant/availability",
  );
  const modelLabel = availability.body?.model_label ?? "unknown";
  if (availability.body?.enabled !== true) {
    console.error(`assistant is disabled on ${API_BASE} — nothing to test`);
    await teardownWorld(world);
    process.exit(2);
  }
  console.log(`model: ${modelLabel}\nseeded: ${world.main.slug} / ${world.alt.slug} / ${world.bPrivate.slug}\n`);

  let done = 0;
  const outcomes = await pool(selected, args.concurrency, async (def, index) => {
    const outcome = await runScenario(def, world, index);
    done++;
    const mark = { pass: "PASS", fail: "FAIL", "soft-fail": "SOFT", error: "ERR " }[outcome.status];
    console.log(`[${String(done).padStart(2)}/${selected.length}] ${mark} ${outcome.id} (${outcome.seconds}s) — ${outcome.detail.slice(0, 150)}`);
    return outcome;
  });

  const teardownProblems = args.keep ? ["skipped (--no-cleanup)"] : await teardownWorld(world);

  const meta: RunMeta = {
    runId,
    startedAt,
    seconds: Math.round((Date.now() - started) / 1000),
    apiBase: API_BASE,
    modelLabel,
    gitRevision: gitRevision(),
    concurrency: args.concurrency,
    selection: args.only ? `--only ${args.only}` : "all",
    budgetMinutes: args.budgetMinutes,
    budgetExhausted: budgetExhausted(),
    teardownProblems,
    knownFindings: KNOWN_PRODUCT_FINDINGS,
    leftovers: [
      "User accounts (`agora-scn-*@agora.dev`) are never deleted — the API has no user-delete endpoint. They own nothing after teardown.",
      "Assistant sessions/messages/runs/artifacts are user-scoped, not workspace-scoped. The runner deletes the sessions it opened; a session whose run was still cancelling can survive the DELETE (409) and remain against the throwaway user.",
      "Everything else (workspaces, issues, projects, sprints, labels, comments, memberships, invitations) is removed by the cascading workspace delete.",
    ],
  };

  const report = renderReport(outcomes, meta);
  writeReport(REPORT_PATH, report);

  const hard = outcomes.filter((o) => o.status === "fail").length;
  const errors = outcomes.filter((o) => o.status === "error").length;
  const soft = outcomes.filter((o) => o.status === "soft-fail").length;
  const passed = outcomes.filter((o) => o.status === "pass").length;
  const resends = outcomes.reduce((n, o) => n + o.modelRetries, 0);
  console.log(
    `\n${passed} pass · ${hard} hard fail · ${soft} soft fail · ${errors} error  ` +
      `(${meta.seconds}s, ${resends} model re-send(s)${meta.budgetExhausted ? ", BUDGET EXHAUSTED" : ""})` +
      `\nreport: ${REPORT_PATH}`,
  );
  process.exit(hard + errors > 0 ? 1 : 0);
}

main().catch((err) => {
  console.error("runner crashed:", err);
  process.exit(3);
});
