import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@agora/core/i18n/react";
import { RESOURCES } from "../../locales";

const mockPin = vi.hoisted(() => vi.fn());
const mockUnpin = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());

vi.mock("@agora/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1", slug: "acme", name: "Acme" }),
}));
vi.mock("@agora/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@agora/core/projects/queries", () => ({
  projectListOptions: (wsId: string) => ({
    queryKey: ["projects", wsId, "list"],
    queryFn: async () => [
      { id: "proj-1", title: "Multicard", icon: "📁" },
      { id: "proj-2", title: "Billing", icon: "📁" },
    ],
  }),
}));
vi.mock("@agora/core/reports", () => ({
  usePinArtifact: () => ({ mutate: mockPin, isPending: false }),
  useUnpinArtifact: () => ({ mutate: mockUnpin, isPending: false }),
}));
vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

import { ArtifactPinAction } from "./artifact-pin-action";

function renderAction(artifactId = "art-1") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <I18nProvider locale="en" resources={RESOURCES}>
      <QueryClientProvider client={qc}>
        <ArtifactPinAction artifactId={artifactId} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

describe("ArtifactPinAction", () => {
  it("publishes to the picked project and says who can read it", async () => {
    mockPin.mockImplementation((_vars, opts) => opts.onSuccess({ id: "pin-9" }));

    renderAction();
    fireEvent.click(screen.getByRole("button", { name: "Pin to a project" }));

    // The disclosure consequence is stated in plain words, not implied.
    expect(
      screen.getByText(
        "Everyone in this workspace can read the report, and always sees its latest version.",
      ),
    ).toBeInTheDocument();
    // Nothing to pin to yet — the confirm stays inert until a project is picked.
    expect(screen.getByRole("button", { name: "Pin" })).toBeDisabled();

    fireEvent.click(await screen.findByRole("button", { name: /No project/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Multicard/ }));
    fireEvent.click(screen.getByRole("button", { name: "Pin" }));

    expect(mockPin).toHaveBeenCalledWith(
      { artifactId: "art-1", projectId: "proj-1" },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(mockToastSuccess).toHaveBeenCalledWith("Pinned to Multicard");
    // Session-local pinned state: the header action flips to Unpin.
    expect(screen.getByRole("button", { name: "Unpin this report" })).toBeInTheDocument();
  });

  it("offers Unpin against the pin it just created", async () => {
    mockPin.mockImplementation((_vars, opts) => opts.onSuccess({ id: "pin-9" }));
    mockUnpin.mockImplementation((_vars, opts) => opts.onSuccess());

    renderAction();
    fireEvent.click(screen.getByRole("button", { name: "Pin to a project" }));
    fireEvent.click(await screen.findByRole("button", { name: /No project/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Billing/ }));
    fireEvent.click(screen.getByRole("button", { name: "Pin" }));

    fireEvent.click(screen.getByRole("button", { name: "Unpin this report" }));
    expect(
      screen.getByText("Pinned to Billing. Everyone in this workspace can read it."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Unpin" }));

    expect(mockUnpin).toHaveBeenCalledWith(
      { artifactId: "art-1", pinId: "pin-9", projectId: "proj-2" },
      expect.objectContaining({ onSuccess: expect.any(Function) }),
    );
    expect(mockToastSuccess).toHaveBeenCalledWith("Report unpinned");
    expect(screen.getByRole("button", { name: "Pin to a project" })).toBeInTheDocument();
  });

  it("keeps the Unpin offer off when the create response carried no usable id", async () => {
    // A drifted 200 the client normalized to EMPTY_REPORT_PIN: the pin exists
    // server-side but this build can't address it, so it must not pretend it can.
    mockPin.mockImplementation((_vars, opts) => opts.onSuccess({ id: "" }));

    renderAction();
    fireEvent.click(screen.getByRole("button", { name: "Pin to a project" }));
    fireEvent.click(await screen.findByRole("button", { name: /No project/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Multicard/ }));
    fireEvent.click(screen.getByRole("button", { name: "Pin" }));

    expect(mockToastSuccess).toHaveBeenCalledWith("Pinned to Multicard");
    expect(screen.getByRole("button", { name: "Pin to a project" })).toBeInTheDocument();
  });

  it("surfaces a failure as an error toast and stays unpinned", async () => {
    mockPin.mockImplementation((_vars, opts) => opts.onError(new Error("nope")));

    renderAction();
    fireEvent.click(screen.getByRole("button", { name: "Pin to a project" }));
    fireEvent.click(await screen.findByRole("button", { name: /No project/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Multicard/ }));
    fireEvent.click(screen.getByRole("button", { name: "Pin" }));

    expect(mockToastError).toHaveBeenCalledWith("Couldn't pin this report");
    // The dialog stays open on failure (the header button is behind the modal
    // and out of the a11y tree), still offering the pick-and-pin flow rather
    // than claiming a pin that never happened.
    expect(screen.getByRole("button", { name: "Pin" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Unpin" })).not.toBeInTheDocument();
  });
});
