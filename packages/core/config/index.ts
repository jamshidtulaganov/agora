import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";

interface ConfigState {
  cdnDomain: string;
  allowSignup: boolean;
  googleClientId: string;
  telegramBotUsername: string;
  daemonServerUrl: string;
  daemonAppUrl: string;
  // Self-host gate (#3433): when true, every "Create workspace" affordance
  // must be hidden. Defaults to false so unknown / older servers behave like
  // the managed-cloud case.
  workspaceCreationDisabled: boolean;
  // SD fork: when true, the login page shows ONLY the Telegram path — the
  // email send-code form, the Google button, and the "or" divider are all
  // hidden. Defaults to false so unknown / older servers keep every method.
  telegramOnly: boolean;
  // Remote Boxes (opt-in): when true, the runtimes page shows the onboarding UI
  // for per-developer remote dev servers. Defaults to false so older servers
  // (and deployments with the feature off) hide it entirely.
  remoteBoxesEnabled: boolean;
  setCdnDomain: (domain: string) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    googleClientId?: string;
    telegramBotUsername?: string;
    workspaceCreationDisabled?: boolean;
    telegramOnly?: boolean;
    remoteBoxesEnabled?: boolean;
  }) => void;
  setDaemonConfig: (config: {
    daemonServerUrl?: string;
    daemonAppUrl?: string;
  }) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  allowSignup: true,
  googleClientId: "",
  telegramBotUsername: "",
  daemonServerUrl: "",
  daemonAppUrl: "",
  workspaceCreationDisabled: false,
  telegramOnly: false,
  remoteBoxesEnabled: false,
  setCdnDomain: (domain) => set({ cdnDomain: domain }),
  setAuthConfig: ({
    allowSignup,
    googleClientId = "",
    telegramBotUsername = "",
    workspaceCreationDisabled = false,
    telegramOnly = false,
    remoteBoxesEnabled = false,
  }) =>
    set({
      allowSignup,
      googleClientId,
      telegramBotUsername,
      workspaceCreationDisabled,
      telegramOnly,
      remoteBoxesEnabled,
    }),
  setDaemonConfig: ({ daemonServerUrl = "", daemonAppUrl = "" }) =>
    set({ daemonServerUrl, daemonAppUrl }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}
