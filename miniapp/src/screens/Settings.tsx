import { api } from "../api";
import { dateRange } from "../format";
import { useNavigation, useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { Card } from "../components/ui";
import type { NotificationPreferences } from "../types";

const CATEGORIES: { key: keyof NotificationPreferences; label: string; hint: string }[] = [
  { key: "trip_updates", label: "Trip updates", hint: "Somebody joins, an event moves" },
  { key: "decisions", label: "Decisions", hint: "New questions and outcomes" },
  { key: "reminders", label: "Reminders", hint: "What is happening tomorrow" },
  { key: "checklist", label: "Checklist", hint: "Items assigned to you" },
  { key: "expenses", label: "Expenses", hint: "Every expense somebody records" },
];

/** Trip facts, the activity feed, and per-user notification settings. */
export function Settings() {
  const tripId = useParam("tripId");
  const nav = useNavigation();
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const prefs = useAsync(() => api.preferences.get(), []);
  const activity = useAsync(() => api.trips.activity(tripId), [tripId]);

  const toggle = async (key: keyof NotificationPreferences, value: boolean) => {
    prefs.set((current) => ({ ...current, [key]: value }));
    try {
      await api.preferences.save({ [key]: value });
    } catch {
      prefs.reload();
    }
  };

  return (
    <Screen title="Settings">
      <Loaded state={trip} skeletonRows={2}>
        {(data) => (
          <Card>
            <div className="title">{data.trip.title}</div>
            <div className="muted">{dateRange(data.trip.start_date, data.trip.end_date)}</div>
            <div className="divider" />
            <div className="card-row">
              <span className="muted">Timezone</span>
              <span>{data.trip.timezone}</span>
            </div>
            <div className="card-row">
              <span className="muted">Currency</span>
              <span>{data.trip.currency}</span>
            </div>
            <div className="card-row">
              <span className="muted">Status</span>
              <span>{data.trip.status}</span>
            </div>
            <div className="card-row">
              <span className="muted">Your role</span>
              <span>{data.me.role}</span>
            </div>
          </Card>
        )}
      </Loaded>

      <div className="section-label">Notify me about</div>
      <Loaded state={prefs} skeletonRows={1}>
        {(current) => (
          <Card>
            {CATEGORIES.map(({ key, label, hint }) => (
              <label key={key} className="card-row" style={{ cursor: "pointer" }}>
                <span>
                  <div>{label}</div>
                  <div className="tiny">{hint}</div>
                </span>
                <input
                  type="checkbox"
                  checked={Boolean(current[key])}
                  onChange={(e) => void toggle(key, e.target.checked)}
                  style={{ width: 22, height: 22, minHeight: 22, flex: "none" }}
                />
              </label>
            ))}
            <p className="tiny">
              These apply to every trip. TripOps only messages you when something needs you.
            </p>
          </Card>
        )}
      </Loaded>

      <div className="section-label">Recent activity</div>
      <Loaded state={activity} skeletonRows={1}>
        {(entries) =>
          entries.length === 0 ? (
            <Card tight>
              <span className="muted">Nothing has happened yet.</span>
            </Card>
          ) : (
            <Card tight>
              {entries.slice(0, 20).map((entry) => (
                <div key={entry.id} className="tiny">
                  {entry.message}
                </div>
              ))}
            </Card>
          )
        }
      </Loaded>

      <button type="button" className="btn ghost" onClick={() => nav.reset({ name: "trips" })}>
        ‹ All trips
      </button>
    </Screen>
  );
}
