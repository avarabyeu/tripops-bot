import { useEffect, useMemo } from "react";
import { NavigationProvider, useNavigation, type Route } from "./router";
import { ready, startParam, insideTelegram } from "./telegram";
import { Accommodation } from "./screens/Accommodation";
import { Balances } from "./screens/Balances";
import { Checklists } from "./screens/Checklists";
import { Decisions } from "./screens/Decisions";
import { ExpenseReport } from "./screens/ExpenseReport";
import { Expenses } from "./screens/Expenses";
import { Join } from "./screens/Join";
import { Logistics } from "./screens/Logistics";
import { People } from "./screens/People";
import { Settings } from "./screens/Settings";
import { Timeline } from "./screens/Timeline";
import { TripHome } from "./screens/TripHome";
import { TripList } from "./screens/TripList";

const SCREENS: Record<string, () => React.ReactElement> = {
  trips: TripList,
  trip: TripHome,
  timeline: Timeline,
  people: People,
  logistics: Logistics,
  accommodation: Accommodation,
  decisions: Decisions,
  expenses: Expenses,
  "expense-report": ExpenseReport,
  balances: Balances,
  checklists: Checklists,
  settings: Settings,
  join: Join,
};

export function App() {
  useEffect(ready, []);

  // A deep link decides the first screen: tapping an invite in a chat must
  // land on the invite, not on a trip list the user has to search through.
  const initial = useMemo<Route>((): Route => {
    const param = startParam() ?? paramFromQuery();
    if (param?.startsWith("inv_")) {
      return { name: "join", params: { token: param.slice(4) } };
    }
    if (param?.startsWith("trip_")) {
      return { name: "trip", params: { tripId: param.slice(5) } };
    }
    return { name: "trips" };
  }, []);

  return (
    <NavigationProvider initial={initial}>
      {!insideTelegram() && <DevBanner />}
      <CurrentScreen />
    </NavigationProvider>
  );
}

function CurrentScreen() {
  const { route } = useNavigation();
  const Screen = SCREENS[route.name] ?? TripList;
  // Remounting on navigation keeps each screen's state local and simple; these
  // screens each load one small thing, so there is nothing worth preserving.
  return <Screen key={`${route.name}:${JSON.stringify(route.params ?? {})}`} />;
}

/** Supports ?startapp=… when the app is opened outside Telegram. */
function paramFromQuery(): string | undefined {
  const params = new URLSearchParams(window.location.search);
  return params.get("startapp") ?? params.get("tgWebAppStartParam") ?? undefined;
}

/**
 * Opened in a browser there is no signed init data, so the API will reject
 * everything unless the backend is running in development with
 * DEV_USER_TELEGRAM_ID set. Saying so beats a wall of 401s.
 */
function DevBanner() {
  return (
    <div className="screen" style={{ paddingBottom: 0 }}>
      <div className="attention warning">
        <span>🛠</span>
        <div>
          <div className="title" style={{ fontSize: 15 }}>
            Running outside Telegram
          </div>
          <div className="tiny">
            Requests are unsigned. Set <code>DEV_USER_TELEGRAM_ID</code> on the backend to browse as
            a test user.
          </div>
        </div>
      </div>
    </div>
  );
}
