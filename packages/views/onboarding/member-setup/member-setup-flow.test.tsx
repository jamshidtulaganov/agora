import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@agora/core/i18n/react";
import type { Workspace } from "@agora/core/types";
import { RESOURCES } from "../../locales";

const mockComplete = vi.hoisted(() => vi.fn());
const mockStartTour = vi.hoisted(() => vi.fn());
const mockUpdateMe = vi.hoisted(() => vi.fn());
const authState = vi.hoisted(() => ({
  user: { id: "u1", name: "Dina Carter", email: "dina.c@tsst.ai", avatar_url: null as string | null },
  setUser: vi.fn(),
}));

vi.mock("@agora/core/onboarding", () => ({
  completeOnboarding: mockComplete,
  useProductTourStore: (selector: (s: { start: typeof mockStartTour }) => unknown) =>
    selector({ start: mockStartTour }),
}));
vi.mock("@agora/core/auth", () => ({
  useAuthStore: Object.assign(
    (selector: (s: typeof authState) => unknown) => selector(authState),
    { getState: () => authState },
  ),
}));
vi.mock("@agora/core/api", () => ({ api: { updateMe: mockUpdateMe } }));
// The notification and avatar controls have their own tests; here they only
// need to be on screen.
vi.mock("../../settings/components/browser-notification-setting", () => ({
  BrowserNotificationSetting: () => <div>browser notifications</div>,
}));
vi.mock("../../settings/components/telegram-notification-setting", () => ({
  TelegramNotificationSetting: () => <div>telegram notifications</div>,
}));
vi.mock("../../settings/components/profile-avatar-picker", () => ({
  ProfileAvatarPicker: () => <button type="button">photo</button>,
}));
vi.mock("@agora/views/platform", () => ({ DragStrip: () => null }));

import { MemberSetupFlow } from "./member-setup-flow";

const WORKSPACES = [
  { id: "ws-1", name: "Customer Experience Team Q3", slug: "customer-experience-team-q3", settings: { zoho_project_id: "1" } },
  { id: "ws-2", name: "Retention Department Q3", slug: "retention-department-q3", settings: { zoho_project_id: "2" } },
] as unknown as Workspace[];

function renderFlow(onComplete = vi.fn()) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={RESOURCES}>{children}</I18nProvider>
  );
  render(<MemberSetupFlow workspaces={WORKSPACES} onComplete={onComplete} />, { wrapper });
  return onComplete;
}

beforeEach(() => {
  vi.clearAllMocks();
  authState.user.name = "Dina Carter";
  mockComplete.mockResolvedValue(undefined);
  mockUpdateMe.mockImplementation(async (patch: { name: string }) => ({ ...authState.user, ...patch }));
});

describe("MemberSetupFlow", () => {
  it("walks what Agora is, profile, notifications and workspaces, then opens the chosen one with a tour", async () => {
    const onComplete = renderFlow();

    expect(screen.getByRole("heading", { name: "Your team is on Agora now" })).toBeInTheDocument();
    expect(screen.getByText(/your 2 workspaces are ready/)).toBeInTheDocument();
    expect(screen.getByText("My Issues")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Set up my profile/ }));

    expect(screen.getByRole("heading", { name: "How your team sees you" })).toBeInTheDocument();
    const name = screen.getByLabelText("Name");
    expect(name).toHaveValue("Dina Carter");
    await userEvent.clear(name);
    await userEvent.type(name, "Dina C.");
    await userEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await waitFor(() => expect(mockUpdateMe).toHaveBeenCalledWith({ name: "Dina C." }));

    expect(await screen.findByRole("heading", { name: "Choose how Agora reaches you" })).toBeInTheDocument();
    expect(screen.getByText("telegram notifications")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("heading", { name: "Your workspaces" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("radio", { name: /Retention Department Q3/ }));
    await userEvent.click(screen.getByRole("button", { name: "Open Retention Department Q3" }));

    await waitFor(() => expect(mockComplete).toHaveBeenCalledWith("member_setup", "ws-2"));
    expect(mockStartTour).toHaveBeenCalledWith("ws-2");
    expect(onComplete).toHaveBeenCalledWith(WORKSPACES[1]);
  });

  it("stays on the profile step when the name is empty, and skips the save when it didn't change", async () => {
    renderFlow();
    await userEvent.click(screen.getByRole("button", { name: /Set up my profile/ }));
    await userEvent.clear(screen.getByLabelText("Name"));
    await userEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("heading", { name: "How your team sees you" })).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("Name"), "Dina Carter");
    await userEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByRole("heading", { name: "Choose how Agora reaches you" })).toBeInTheDocument();
    expect(mockUpdateMe).not.toHaveBeenCalled();
  });

  it("finishes without a tour when the person turns it off, and goes back a step", async () => {
    const onComplete = renderFlow();
    await userEvent.click(screen.getByRole("button", { name: /Set up my profile/ }));
    await userEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await screen.findByRole("heading", { name: "Choose how Agora reaches you" });
    await userEvent.click(screen.getByRole("button", { name: /Back/ }));
    expect(screen.getByRole("heading", { name: "How your team sees you" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await userEvent.click(await screen.findByRole("button", { name: /Continue/ }));

    await userEvent.click(screen.getByRole("switch"));
    await userEvent.click(screen.getByRole("button", { name: "Open Customer Experience Team Q3" }));

    await waitFor(() => expect(onComplete).toHaveBeenCalledWith(WORKSPACES[0]));
    expect(mockStartTour).not.toHaveBeenCalled();
  });
});
