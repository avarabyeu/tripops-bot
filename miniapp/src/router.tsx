/**
 * A stack router, roughly 60 lines instead of a dependency.
 *
 * Telegram Mini Apps are navigated with the client's own back button, so what
 * the app needs is a stack it can push onto and pop from — not URL routing.
 * The native back button is wired to `pop` whenever the stack is deeper than
 * one screen.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { setBackButton } from "./telegram";

export interface Route {
  name: string;
  params?: Record<string, string>;
}

interface Navigation {
  route: Route;
  depth: number;
  push(route: Route): void;
  replace(route: Route): void;
  pop(): void;
  reset(route: Route): void;
}

const NavigationContext = createContext<Navigation | null>(null);

export function NavigationProvider({
  initial,
  children,
}: {
  initial: Route;
  children: ReactNode;
}) {
  const [stack, setStack] = useState<Route[]>([initial]);

  const push = useCallback((route: Route) => setStack((s) => [...s, route]), []);
  const replace = useCallback(
    (route: Route) => setStack((s) => [...s.slice(0, -1), route]),
    [],
  );
  const pop = useCallback(
    () => setStack((s) => (s.length > 1 ? s.slice(0, -1) : s)),
    [],
  );
  const reset = useCallback((route: Route) => setStack([route]), []);

  // The Telegram back button is the only back affordance; the app draws none.
  useEffect(() => setBackButton(stack.length > 1, pop), [stack.length, pop]);

  // In a browser, the hardware/browser back should behave the same way.
  useEffect(() => {
    const onPopState = (e: PopStateEvent) => {
      e.preventDefault();
      pop();
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [pop]);

  const value = useMemo<Navigation>(
    () => ({
      route: stack[stack.length - 1] ?? initial,
      depth: stack.length,
      push,
      replace,
      pop,
      reset,
    }),
    [stack, initial, push, replace, pop, reset],
  );

  return <NavigationContext.Provider value={value}>{children}</NavigationContext.Provider>;
}

export function useNavigation(): Navigation {
  const nav = useContext(NavigationContext);
  if (!nav) throw new Error("useNavigation used outside a NavigationProvider");
  return nav;
}

/** Reads a required route parameter. */
export function useParam(name: string): string {
  const { route } = useNavigation();
  const value = route.params?.[name];
  if (!value) throw new Error(`route ${route.name} is missing the ${name} parameter`);
  return value;
}
