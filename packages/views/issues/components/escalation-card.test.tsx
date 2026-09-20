import { fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "@agora/core/i18n/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import enIssues from "../../locales/en/issues.json";
import { EscalationCard } from "./escalation-card";
import type { Escalation } from "@agora/core/types";

// The escalation card is the surface a stopped run depends on: if it does not
// render, or its answer does not reach the server, the run stays parked
// forever. These tests pin the three things that make it work — the ask is
// visible, an option is an answer, and free text is an answer — plus the two
// ways it must degrade quietly (no open escalation, drifted response).

const mocks = vi.hoisted(() => ({
  useQuery: vi.fn(),
  mutate: vi.fn(),
  isPending: false,
}));

vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return { ...actual, useQuery: mocks.useQuery };
});

vi.mock("@agora/core/issues/queries", () => ({
  issueEscalationsOptions: (issueId: string) => ({
    queryKey: ["issues", "escalations", issueId],
  }),
}));

vi.mock("@agora/core/issues/mutations", () => ({
  useResolveEscalation: () => ({ mutate: mocks.mutate, isPending: mocks.isPending }),
}));

vi.mock("@agora/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Rex" }),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

function escalation(overrides: Partial<Escalation> = {}): Escalation {
  return {
    id: "esc-1",
    workspace_id: "ws-1",
    issue_id: "issue-1",
    task_id: "task-1",
    agent_id: "agent-1",
    kind: "question",
    prompt: "Should the export be CSV or XLSX?",
    detail: "Read the issue and the linked PR; neither says.",
    options: [],
    risk_tier: "",
    status: "open",
    answer: "",
    answered_by: "",
    answered_at: "",
    resumed_task_id: "",
    raised_at: "2026-09-20T10:00:00Z",
    ...overrides,
  };
}

function renderCard() {
  return render(
    <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
      <EscalationCard issueId="issue-1" />
    </I18nProvider>,
  );
}

describe("EscalationCard", () => {
  beforeEach(() => {
    mocks.mutate.mockReset();
    mocks.isPending = false;
  });

  it("shows the ask, who is waiting, and what was already tried", () => {
    mocks.useQuery.mockReturnValue({ data: [escalation()] });
    renderCard();

    expect(screen.getByText("Should the export be CSV or XLSX?")).toBeInTheDocument();
    expect(screen.getByText("Rex is waiting on you")).toBeInTheDocument();
    expect(
      screen.getByText(/Read the issue and the linked PR; neither says\./),
    ).toBeInTheDocument();
  });

  it("sends free text as the answer", () => {
    mocks.useQuery.mockReturnValue({ data: [escalation()] });
    renderCard();

    fireEvent.change(screen.getByPlaceholderText("Answer, or tell it what to do instead…"), {
      target: { value: "  CSV, keep the header row.  " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Send answer" }));

    expect(mocks.mutate).toHaveBeenCalledTimes(1);
    expect(mocks.mutate.mock.calls[0]![0]).toEqual({
      escalationId: "esc-1",
      issueId: "issue-1",
      answer: "CSV, keep the header row.",
    });
  });

  it("treats a tapped option as the answer — one tap, no typing", () => {
    mocks.useQuery.mockReturnValue({ data: [escalation({ options: ["CSV", "XLSX"] })] });
    renderCard();

    fireEvent.click(screen.getByRole("button", { name: "XLSX" }));

    expect(mocks.mutate.mock.calls[0]![0]).toMatchObject({ answer: "XLSX" });
  });

  it("keeps the free-text box available alongside options", () => {
    mocks.useQuery.mockReturnValue({ data: [escalation({ options: ["CSV", "XLSX"] })] });
    renderCard();

    // "Neither — do this instead" has to remain possible; options are a
    // shortcut, not a constraint.
    expect(
      screen.getByPlaceholderText("Answer, or tell it what to do instead…"),
    ).toBeInTheDocument();
  });

  it("refuses to send an empty answer", () => {
    mocks.useQuery.mockReturnValue({ data: [escalation()] });
    renderCard();

    const send = screen.getByRole("button", { name: "Send answer" });
    expect(send).toBeDisabled();
    fireEvent.click(send);
    expect(mocks.mutate).not.toHaveBeenCalled();
  });

  it("renders nothing when the only escalations are already resolved", () => {
    mocks.useQuery.mockReturnValue({
      data: [escalation({ status: "answered" }), escalation({ id: "esc-2", status: "cancelled" })],
    });
    const { container } = renderCard();
    expect(container).toBeEmptyDOMElement();
  });

  // The parse fallback for a drifted response is an empty list, and an
  // undefined data field is what a still-loading query hands us. Both must
  // render nothing rather than an empty shell.
  it("renders nothing for an empty or missing list", () => {
    mocks.useQuery.mockReturnValue({ data: [] });
    expect(renderCard().container).toBeEmptyDOMElement();

    mocks.useQuery.mockReturnValue({ data: undefined });
    expect(renderCard().container).toBeEmptyDOMElement();
  });

  // A row that survived parsing but carries no id is not addressable — the
  // resolve endpoint would 404. Render nothing instead of a dead button.
  it("renders nothing for an escalation with no id", () => {
    mocks.useQuery.mockReturnValue({ data: [escalation({ id: "" })] });
    expect(renderCard().container).toBeEmptyDOMElement();
  });
});
