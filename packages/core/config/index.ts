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
  // Integration availability (mirror the backend env gates via /api/config).
  // Each gates the corresponding Settings → Integrations section plus the
  // connector-specific issue/project surfaces. Default false so a deployment
  // without the integration (and older servers) shows a clean Integrations tab.
  bitrixEnabled: boolean;
  zohoEnabled: boolean;
  larkEnabled: boolean;
  setCdnDomain: (domain: string) => void;
  setAuthConfig: (config: {
    allowSignup: boolean;
    googleClientId?: string;
    telegramBotUsername?: string;
    workspaceCreationDisabled?: boolean;
    telegramOnly?: boolean;
    bitrixEnabled?: boolean;
    zohoEnabled?: boolean;
    larkEnabled?: boolean;
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
  bitrixEnabled: false,
  zohoEnabled: false,
  larkEnabled: false,
  setCdnDomain: (domain) => set({ cdnDomain: domain }),
  setAuthConfig: ({
    allowSignup,
    googleClientId = "",
    telegramBotUsername = "",
    workspaceCreationDisabled = false,
    telegramOnly = false,
    bitrixEnabled = false,
    zohoEnabled = false,
    larkEnabled = false,
  }) =>
    set({
      allowSignup,
      googleClientId,
      telegramBotUsername,
      workspaceCreationDisabled,
      telegramOnly,
      bitrixEnabled,
      zohoEnabled,
      larkEnabled,
    }),
  setDaemonConfig: ({ daemonServerUrl = "", daemonAppUrl = "" }) =>
    set({ daemonServerUrl, daemonAppUrl }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}
