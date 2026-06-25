import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import {
  NavigationProvider,
  type NavigationAdapter,
} from "@agora/views/navigation";

// In-memory router for the Mini App. The SPA has a tiny, fixed screen set
// (three tabs + issue detail + create), so a typed route union beats a
// path-string router — no URL bar inside Telegram. A NavigationAdapter is also
// provided so any @agora/views/@agora/core component that calls
// useNavigation().push() resolves an issue path to the right screen instead of
// throwing.

export type Tab = "inbox" | "issues" | "chat";

export type Route =
  | { name: "tab"; tab: Tab }
  | { name: "issue"; id: string }
  | { name: "create" }
  | { name: "chat-session"; id: string };

interface RouterValue {
  route: Route;
  navigate: (route: Route) => void;
  back: () => void;
  activeTab: Tab;
  openTab: (tab: Tab) => void;
}

const RouterContext = createContext<RouterValue | null>(null);

const DEFAULT_ROUTE: Route = { name: "tab", tab: "inbox" };

export function RouterProvider({
  initialRoute,
  children,
}: {
  initialRoute?: Route;
  children: ReactNode;
}) {
  const [stack, setStack] = useState<Route[]>([initialRoute ?? DEFAULT_ROUTE]);
  const [lastTab, setLastTab] = useState<Tab>(
    initialRoute?.name === "tab" ? initialRoute.tab : "inbox",
  );

  const route = stack[stack.length - 1] ?? DEFAULT_ROUTE;

  const navigate = useCallback((next: Route) => {
    if (next.name === "tab") setLastTab(next.tab);
    setStack((s) => [...s, next]);
  }, []);

  const back = useCallback(() => {
    setStack((s) => (s.length > 1 ? s.slice(0, -1) : s));
  }, []);

  // Switching tabs resets the stack to that tab (tabs are roots, not pushes).
  const openTab = useCallback((tab: Tab) => {
    setLastTab(tab);
    setStack([{ name: "tab", tab }]);
  }, []);

  const value = useMemo<RouterValue>(
    () => ({ route, navigate, back, activeTab: lastTab, openTab }),
    [route, navigate, back, lastTab, openTab],
  );

  const adapter = useMemo<NavigationAdapter>(
    () => makeAdapter(value),
    [value],
  );

  return (
    <RouterContext.Provider value={value}>
      <NavigationProvider value={adapter}>{children}</NavigationProvider>
    </RouterContext.Provider>
  );
}

export function useRouter(): RouterValue {
  const ctx = useContext(RouterContext);
  if (!ctx) throw new Error("useRouter must be used within RouterProvider");
  return ctx;
}

// Bridge a path-string push() (from shared components) onto the typed router.
// Workspace issue paths look like /<slug>/issues/<id>; extract the id and open
// the issue screen. Anything else is a no-op (the Mini App has no other deep
// destinations). pathname/searchParams are stubbed since there is no URL bar.
function makeAdapter(router: RouterValue): NavigationAdapter {
  const routeToPath = (r: Route): string => {
    switch (r.name) {
      case "issue":
        return `/issues/${r.id}`;
      case "create":
        return "/issues/new";
      case "chat-session":
        return `/chat/${r.id}`;
      case "tab":
        return `/${r.tab}`;
    }
  };
  return {
    push: (path: string) => {
      const m = /\/issues\/([0-9a-fA-F-]{8,})/.exec(path);
      if (m && m[1]) router.navigate({ name: "issue", id: m[1] });
    },
    replace: (path: string) => {
      const m = /\/issues\/([0-9a-fA-F-]{8,})/.exec(path);
      if (m && m[1]) router.navigate({ name: "issue", id: m[1] });
    },
    back: () => router.back(),
    pathname: routeToPath(router.route),
    searchParams: new URLSearchParams(),
    getShareableUrl: (path: string) =>
      typeof window !== "undefined" ? window.location.origin + path : path,
  };
}
