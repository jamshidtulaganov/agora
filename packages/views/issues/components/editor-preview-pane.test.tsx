// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  EditorPreviewPane,
  parseTestOutput,
  type TestRunState,
} from "./editor-preview-pane";

// The pane talks to the daemon directly over fetch — stub it per URL suffix.
// Payloads mimic the daemon's /editor/preview/status and /editor/test bodies.
const fetchMock = vi.fn();

function stubDaemon({
  status = { running: false },
  test = { command: "npm run test", passed: true, exit_code: 0, output: "" },
}: {
  status?: Record<string, unknown>;
  test?: Record<string, unknown>;
} = {}) {
  fetchMock.mockImplementation((url: string) => {
    const payload = String(url).endsWith("/editor/test") ? test : status;
    return Promise.resolve({
      ok: true,
      json: () => Promise.resolve(payload),
    });
  });
}

function doneTestRunState(overrides: Partial<TestRunState> = {}): TestRunState {
  return {
    testState: "done",
    testOut: "",
    testPassed: true,
    testCmd: "",
    parsedTests: { failed: [], failedCount: 0, passedCount: 0 },
    ...overrides,
  };
}

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const commandInput = () =>
  screen.getByPlaceholderText("dev command (e.g. npm run dev)") as HTMLInputElement;

describe("EditorPreviewPane command prefill", () => {
  it("prefers the project default over the daemon-detected command", async () => {
    stubDaemon({ status: { running: false, detected: "npm run dev" } });
    render(
      <EditorPreviewPane
        daemonUrl="http://127.0.0.1:9999"
        workdir="/tmp/wt"
        defaultDevCommand="make dev"
      />,
    );
    await waitFor(() => expect(commandInput().value).toBe("make dev"));
  });

  it("a running preview's actual command wins over the project default", async () => {
    stubDaemon({
      status: {
        running: true,
        command: "pnpm dev",
        url: "http://127.0.0.1:5173/",
        port: 5173,
      },
    });
    render(
      <EditorPreviewPane
        daemonUrl="http://127.0.0.1:9999"
        workdir="/tmp/wt"
        defaultDevCommand="make dev"
      />,
    );
    await waitFor(() => expect(commandInput().value).toBe("pnpm dev"));
  });

  it("falls back to the daemon-detected command without a project default", async () => {
    stubDaemon({ status: { running: false, detected: "npm run dev" } });
    render(
      <EditorPreviewPane daemonUrl="http://127.0.0.1:9999" workdir="/tmp/wt" />,
    );
    await waitFor(() => expect(commandInput().value).toBe("npm run dev"));
  });
});

describe("EditorPreviewPane test runs", () => {
  it("posts the project test command to /editor/test", async () => {
    stubDaemon({
      test: { command: "make test", passed: true, exit_code: 0, output: "ok" },
    });
    const onTestResult = vi.fn();
    render(
      <EditorPreviewPane
        daemonUrl="http://127.0.0.1:9999"
        workdir="/tmp/wt"
        defaultTestCommand="make test"
        testRunState={doneTestRunState()}
        onTestResult={onTestResult}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /run tests/i }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url]) =>
        String(url).endsWith("/editor/test"),
      );
      expect(call).toBeTruthy();
      expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({
        workdir: "/tmp/wt",
        command: "make test",
      });
    });
    await waitFor(() =>
      expect(onTestResult).toHaveBeenCalledWith(
        expect.objectContaining({ testState: "done", testCmd: "make test" }),
      ),
    );
  });

  it("posts an empty command (daemon auto-detect) without a project override", async () => {
    stubDaemon();
    render(
      <EditorPreviewPane
        daemonUrl="http://127.0.0.1:9999"
        workdir="/tmp/wt"
        testRunState={doneTestRunState()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /run tests/i }));
    await waitFor(() => {
      const call = fetchMock.mock.calls.find(([url]) =>
        String(url).endsWith("/editor/test"),
      );
      expect(call).toBeTruthy();
      expect(JSON.parse((call![1] as RequestInit).body as string)).toEqual({
        workdir: "/tmp/wt",
        command: "",
      });
    });
  });
});

describe("EditorPreviewPane zero-parsed-cases rendering", () => {
  it("renders 'Tests passed (exit 0)' when exit 0 parsed zero cases", async () => {
    stubDaemon();
    render(
      <EditorPreviewPane
        daemonUrl="http://127.0.0.1:9999"
        workdir="/tmp/wt"
        testRunState={doneTestRunState({
          // go test / phpunit output — nothing the vitest-shaped parser matches.
          testOut: "ok  \texample.com/x\t0.4s",
          parsedTests: parseTestOutput("ok  \texample.com/x\t0.4s"),
        })}
      />,
    );
    expect(await screen.findByText(/Tests passed \(exit 0\)/)).toBeInTheDocument();
    expect(screen.queryByText(/All 0 tests passed/)).toBeNull();
  });

  it("keeps the per-count summary when cases were parsed", async () => {
    stubDaemon();
    const out = "Tests  12 passed";
    render(
      <EditorPreviewPane
        daemonUrl="http://127.0.0.1:9999"
        workdir="/tmp/wt"
        testRunState={doneTestRunState({
          testOut: out,
          parsedTests: parseTestOutput(out),
        })}
      />,
    );
    expect(await screen.findByText("All 12 tests passed ✓")).toBeInTheDocument();
  });
});
