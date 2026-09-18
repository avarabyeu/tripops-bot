import { useState } from "react";
import { api, ApiError } from "../api";
import { CURRENCIES, dateRange, timezones } from "../format";
import { useNavigation, useParam } from "../router";
import { confirm } from "../telegram";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Field, Sheet } from "../components/ui";
import type { NotificationPreferences, Trip } from "../types";

const CATEGORIES: { key: keyof NotificationPreferences; label: string; hint: string }[] = [
  { key: "trip_updates", label: "Trip updates", hint: "Somebody joins, an event moves" },
  { key: "decisions", label: "Decisions", hint: "New questions and outcomes" },
  { key: "reminders", label: "Reminders", hint: "What is happening tomorrow, and settling up after" },
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
  // Only for the expense count: the currency stops being editable once money
  // has been recorded against it, and the form says so rather than letting
  // the save fail.
  const dashboard = useAsync(() => api.trips.dashboard(tripId), [tripId]);

  const [editing, setEditing] = useState(false);
  const [deleteError, setDeleteError] = useState<string | undefined>();
  const canEdit = trip.data !== undefined && trip.data.me.role !== "member";

  // Owner only, and irreversible, so it asks twice: Telegram's own confirm
  // dialog, and the trip's name typed back is a step too far for a group of
  // friends — the dialog names what is about to go.
  const removeTrip = async () => {
    const title = trip.data?.trip.title ?? "this trip";
    if (!(await confirm(`Delete "${title}" for everyone? This cannot be undone.`))) return;
    setDeleteError(undefined);
    try {
      await api.trips.remove(tripId);
      // reset, not pop: the screens underneath are all about a trip that no
      // longer exists and would each render their own 404.
      nav.reset({ name: "trips" });
    } catch (err) {
      setDeleteError(err instanceof ApiError ? err.message : "Could not delete the trip.");
    }
  };

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
            {canEdit && (
              <>
                <div className="divider" />
                <Button block variant="secondary" onClick={() => setEditing(true)}>
                  Edit trip
                </Button>
              </>
            )}
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

      {trip.data?.me.role === "owner" && (
        <>
          <div className="section-label">Danger zone</div>
          <Card>
            <div className="card-row">
              <span>
                <div>Delete this trip</div>
                <div className="tiny">
                  Everything goes with it: people, timeline, decisions, logistics, checklists and
                  the whole ledger. There is no undo.
                </div>
              </span>
            </div>
            {deleteError && <span className="field-error">{deleteError}</span>}
            <AsyncButton block variant="danger" onClick={removeTrip}>
              Delete trip
            </AsyncButton>
          </Card>
        </>
      )}

      <button type="button" className="btn ghost" onClick={() => nav.reset({ name: "trips" })}>
        ‹ All trips
      </button>

      {editing && trip.data && (
        <EditTripSheet
          trip={trip.data.trip}
          expenseCount={dashboard.data?.expenses.count ?? 0}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            trip.reload();
            activity.reload();
          }}
        />
      )}
    </Screen>
  );
}

/**
 * The five facts a trip is made of.
 *
 * A trip created through the bot takes two answers — a title and dates — and
 * inherits the server's default timezone and currency. This is where a wrong
 * default gets fixed, which is most of why the screen exists.
 */
function EditTripSheet({
  trip,
  expenseCount,
  onClose,
  onSaved,
}: {
  trip: Trip;
  expenseCount: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [title, setTitle] = useState(trip.title);
  const [description, setDescription] = useState(trip.description);
  const [start, setStart] = useState(trip.start_date);
  const [end, setEnd] = useState(trip.end_date);
  const [timezone, setTimezone] = useState(trip.timezone);
  const [currency, setCurrency] = useState(trip.currency);
  const [error, setError] = useState<ApiError | undefined>();

  // Every expense is stored in minor units of the trip currency with no rate
  // history, so changing it later would silently reinterpret all of them. The
  // backend refuses; the form does not offer.
  const currencyLocked = expenseCount > 0;
  const valid = title.trim().length >= 2 && start !== "" && end !== "" && end >= start;

  const submit = async () => {
    setError(undefined);
    try {
      await api.trips.update(trip.id, {
        title,
        description,
        start_date: start,
        end_date: end,
        timezone,
        ...(currencyLocked ? {} : { currency }),
      });
      onSaved();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="Edit trip" onClose={onClose}>
      <Field label="Title" error={error?.fields.title}>
        <input value={title} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Field label="Description" error={error?.fields.description}>
        <textarea
          rows={2}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="Anything the group should know"
        />
      </Field>
      <div className="row">
        <Field label="From" error={error?.fields.start_date}>
          <input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
        </Field>
        <Field label="To" error={error?.fields.end_date}>
          <input type="date" value={end} min={start} onChange={(e) => setEnd(e.target.value)} />
        </Field>
      </div>
      <Field label="Timezone" error={error?.fields.timezone}>
        <select value={timezone} onChange={(e) => setTimezone(e.target.value)}>
          {timezones().map((tz) => (
            <option key={tz} value={tz}>
              {tz}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Currency" error={error?.fields.currency}>
        <select
          value={currency}
          disabled={currencyLocked}
          onChange={(e) => setCurrency(e.target.value)}
        >
          {CURRENCIES.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      </Field>
      {currencyLocked && (
        <p className="tiny">
          The currency is fixed once money has been recorded — {expenseCount}{" "}
          {expenseCount === 1 ? "expense is" : "expenses are"} already in {trip.currency}.
        </p>
      )}
      <p className="tiny">
        Moving the dates does not move anything on the timeline. Changing the timezone changes the
        times everyone sees, not the times themselves.
      </p>
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={!valid}>
        Save changes
      </AsyncButton>
    </Sheet>
  );
}
