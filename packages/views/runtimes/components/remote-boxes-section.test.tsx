import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

// Control the Remote Boxes hooks so the section renders deterministically and we
// can assert the create/delete wiring without a real backend.
const mutateCreate = vi.fn().mockResolvedValue({ id: "new" });
const mutateDelete = vi.fn().mockResolvedValue(undefined);
let boxesData: unknown[] = [];

const mutateSync = vi.fn().mockResolvedValue({ ok: true, branch: "b", output: "", box: {} });
const mutateProvision = vi.fn().mockResolvedValue({
  handle: "shakhzod",
  subdomain: "shakhzod.sdteam.uz",
  work_dir: "/var/www/shakhzod.sdteam.uz",
  database: "dbt_shakhzod",
  script: "set -e && echo provisioning",
  dry_run: true,
  ran: false,
  ok: false,
  output: "",
  box: null,
});

vi.mock("@agora/core/runtimes", () => ({
  remoteBoxesOptions: (wsId: string) => ({
    queryKey: ["remote-boxes", wsId, "list"],
    queryFn: () => Promise.resolve(boxesData),
  }),
  useCreateRemoteBox: () => ({ mutateAsync: mutateCreate, isPending: false }),
  useDeleteRemoteBox: () => ({ mutateAsync: mutateDelete }),
  useSyncRemoteBox: () => ({ mutateAsync: mutateSync, isPending: false }),
  useProvisionRemoteBox: () => ({ mutateAsync: mutateProvision, isPending: false }),
}));

vi.mock("@agora/core/workspace/queries", () => ({
  memberListOptions: (wsId: string) => ({
    queryKey: ["members", wsId],
    queryFn: () =>
      Promise.resolve([
        { id: "m1", workspace_id: wsId, user_id: "u1", role: "member", created_at: "", name: "Shakhzod", email: "s@x.io", avatar_url: null },
      ]),
  }),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

import { RemoteBoxesSection } from "./remote-boxes-section";

function renderSection() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <RemoteBoxesSection wsId="ws-1" />
    </QueryClientProvider>,
  );
}

describe("RemoteBoxesSection", () => {
  beforeEach(() => {
    mutateCreate.mockClear();
    mutateDelete.mockClear();
    mutateProvision.mockClear();
    boxesData = [];
  });

  it("shows the empty state when there are no boxes", async () => {
    renderSection();
    expect(await screen.findByText(/No remote boxes yet/i)).toBeTruthy();
  });

  it("renders a box with its ssh target and status", async () => {
    boxesData = [
      {
        id: "b1",
        workspace_id: "ws-1",
        owner_id: "u1",
        label: "jamshid",
        ssh_host: "jamshid.sdteam.uz",
        ssh_user: "dev",
        ssh_port: 22,
        deploy_pubkey: "",
        daemon_id: null,
        status: "pending",
        last_error: "",
        created_at: "2026-06-29T00:00:00Z",
      },
    ];
    renderSection();
    expect(await screen.findByText("jamshid")).toBeTruthy();
    expect(screen.getByText("dev@jamshid.sdteam.uz")).toBeTruthy();
    expect(screen.getByText("pending")).toBeTruthy();
  });

  it("submits a new box from the form", async () => {
    renderSection();
    fireEvent.change(await screen.findByLabelText("Box label"), { target: { value: "qa" } });
    fireEvent.change(screen.getByLabelText("SSH host"), { target: { value: "qa.sdteam.uz" } });
    fireEvent.change(screen.getByLabelText("SSH user"), { target: { value: "qa" } });
    fireEvent.click(screen.getByText("Add box"));
    await waitFor(() =>
      expect(mutateCreate).toHaveBeenCalledWith({
        label: "qa",
        ssh_host: "qa.sdteam.uz",
        ssh_user: "qa",
      }),
    );
  });

  it("disables Add until all fields are filled", async () => {
    renderSection();
    const btn = (await screen.findByText("Add box")).closest("button")!;
    expect(btn.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Box label"), { target: { value: "qa" } });
    fireEvent.change(screen.getByLabelText("SSH host"), { target: { value: "qa.sdteam.uz" } });
    fireEvent.change(screen.getByLabelText("SSH user"), { target: { value: "qa" } });
    expect(btn.disabled).toBe(false);
  });

  it("syncs a branch when the box has a repo + work_dir", async () => {
    boxesData = [
      {
        id: "b1", workspace_id: "ws-1", owner_id: null, label: "jamshid",
        ssh_host: "193.149.18.99", ssh_user: "jamshidfr", ssh_port: 33022,
        deploy_pubkey: "", daemon_id: null, status: "online", last_error: "",
        repo_url: "https://github.com/azizkh/sd.git", work_dir: "/var/www/site",
        last_branch: "", created_at: "2026-06-29T00:00:00Z",
      },
    ];
    renderSection();
    const input = await screen.findByLabelText("Branch for jamshid");
    fireEvent.change(input, { target: { value: "btx-32077" } });
    fireEvent.click(screen.getByText("Sync"));
    await waitFor(() =>
      expect(mutateSync).toHaveBeenCalledWith({ id: "b1", branch: "btx-32077" }),
    );
  });

  it("hides the sync row when the box has no repo configured", async () => {
    boxesData = [
      {
        id: "b1", workspace_id: "ws-1", owner_id: null, label: "noconfig",
        ssh_host: "h", ssh_user: "u", ssh_port: 22, deploy_pubkey: "",
        daemon_id: null, status: "pending", last_error: "",
        repo_url: "", work_dir: "", last_branch: "", created_at: "2026-06-29T00:00:00Z",
      },
    ];
    renderSection();
    await screen.findByText("noconfig");
    expect(screen.queryByLabelText("Branch for noconfig")).toBeNull();
  });

  it("previews a per-developer box provision (dry-run) and shows the runbook", async () => {
    renderSection();
    // Wait for the async member list so the option exists before selecting it.
    await screen.findByRole("option", { name: "Shakhzod" });
    fireEvent.change(screen.getByLabelText("Member"), { target: { value: "m1" } });
    fireEvent.click(screen.getByText("Preview"));
    await waitFor(() =>
      expect(mutateProvision).toHaveBeenCalledWith({
        member_id: "m1",
        handle: undefined,
        dry_run: true,
      }),
    );
    // The dry-run preview surfaces the computed placement + the runbook for review.
    expect(await screen.findByText("shakhzod.sdteam.uz")).toBeTruthy();
    expect(screen.getByText(/echo provisioning/)).toBeTruthy();
    expect(screen.getByText("Provision for real")).toBeTruthy();
  });

  it("deletes a box", async () => {
    boxesData = [
      {
        id: "b1", workspace_id: "ws-1", owner_id: null, label: "jamshid",
        ssh_host: "jamshid.sdteam.uz", ssh_user: "dev", ssh_port: 22,
        deploy_pubkey: "", daemon_id: null, status: "online", last_error: "",
        created_at: "2026-06-29T00:00:00Z",
      },
    ];
    renderSection();
    fireEvent.click(await screen.findByLabelText("Remove jamshid"));
    await waitFor(() => expect(mutateDelete).toHaveBeenCalledWith("b1"));
  });
});
