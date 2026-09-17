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
  useRef,
  useState,
  type ReactNode,
} from "react";
import { insideTelegram, setBackButton } from "./telegram";

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
  /** True when the app has to draw its own back control. */
  needsBackControl: boolean;
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

  // Inside Telegram the client owns navigation: its BackButton is the back
  // affordance and there is no address bar. In a browser neither exists, so
  // the stack is mirrored into history and the app draws its own control.
  const inTelegram = insideTelegram();
  // How many history entries this app has pushed, so back can be delegated to
  // the browser without guessing.
  const pushedEntries = useRef(0);

  const popStack = useCallback(
    () => setStack((s) => (s.length > 1 ? s.slice(0, -1) : s)),
    [],
  );

  const push = useCallback(
    (route: Route) => {
      setStack((s) => [...s, route]);
      if (!inTelegram) {
        pushedEntries.current += 1;
        window.history.pushState({ tripops: pushedEntries.current }, "");
      }
    },
    [inTelegram],
  );

  const replace = useCallback(
    (route: Route) => setStack((s) => [...s.slice(0, -1), route]),
    [],
  );

  const pop = useCallback(() => {
    // Going through history keeps the two in step; the listener below does the
    // actual pop when the browser reports it.
    if (!inTelegram && pushedEntries.current > 0) {
      window.history.back();
      return;
    }
    popStack();
  }, [inTelegram, popStack]);

  const reset = useCallback((route: Route) => {
    setStack([route]);
    // The entries already pushed are left alone: at the root, back should do
    // nothing, and that is exactly what popping an empty stack does.
    pushedEntries.current = 0;
  }, []);

  useEffect(() => setBackButton(stack.length > 1, pop), [stack.length, pop]);

  useEffect(() => {
    const onPopState = () => {
      pushedEntries.current = Math.max(0, pushedEntries.current - 1);
      popStack();
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [popStack]);

  const value = useMemo<Navigation>(
    () => ({
      route: stack[stack.length - 1] ?? initial,
      depth: stack.length,
      push,
      replace,
      pop,
      reset,
      needsBackControl: !inTelegram && stack.length > 1,
    }),
    [stack, initial, push, replace, pop, reset, inTelegram],
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
