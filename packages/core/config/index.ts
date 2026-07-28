import { createStore } from "zustand/vanilla";
import { useStore } from "zustand";

interface ConfigState {
  cdnDomain: string;
  allowSignup: boolean;
  googleClientId: string;
  telegramBotUsername: string;
  daemonServerUrl: string;
  daemonAppUrl: string;
  cliReleasesUrl: string;
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
  // Per-agent Telegram bots. Separate from telegramOnly (a login mode) and
  // from the platform bot: an agent bot's token is sealed at rest, so without
  // the seal key no install can succeed and the panel must not offer one.
  telegramBotsEnabled: boolean;
  // True once the /api/config fetch has settled (success OR failure). The
  // login page renders a loading state until then so a telegram_only server
  // never flashes the email/Google form while the response is in flight.
  authConfigLoaded: boolean;
  markAuthConfigLoaded: () => void;
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
    telegramBotsEnabled?: boolean;
  }) => void;
  setDaemonConfig: (config: {
    daemonServerUrl?: string;
    daemonAppUrl?: string;
  }) => void;
  setRuntimeConfig: (config: { cliReleasesUrl?: string }) => void;
}

export const configStore = createStore<ConfigState>((set) => ({
  cdnDomain: "",
  allowSignup: true,
  googleClientId: "",
  telegramBotUsername: "",
  daemonServerUrl: "",
  daemonAppUrl: "",
  cliReleasesUrl: "",
  workspaceCreationDisabled: false,
  telegramOnly: false,
  bitrixEnabled: false,
  zohoEnabled: false,
  larkEnabled: false,
  telegramBotsEnabled: false,
  authConfigLoaded: false,
  markAuthConfigLoaded: () => set({ authConfigLoaded: true }),
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
    telegramBotsEnabled = false,
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
      telegramBotsEnabled,
      authConfigLoaded: true,
    }),
  setDaemonConfig: ({ daemonServerUrl = "", daemonAppUrl = "" }) =>
    set({ daemonServerUrl, daemonAppUrl }),
  setRuntimeConfig: ({ cliReleasesUrl = "" }) => set({ cliReleasesUrl }),
}));

export function useConfigStore(): ConfigState;
export function useConfigStore<T>(selector: (state: ConfigState) => T): T;
export function useConfigStore<T>(selector?: (state: ConfigState) => T) {
  return useStore(configStore, selector as (state: ConfigState) => T);
}
