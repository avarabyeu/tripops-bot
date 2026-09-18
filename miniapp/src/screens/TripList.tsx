import { useState } from "react";
import { api, ApiError } from "../api";
import { CURRENCIES, dateRange } from "../format";
import { useNavigation } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Empty, Field, Sheet } from "../components/ui";
import type { TripSummary } from "../types";

const STATUS_ICONS: Record<string, string> = {
  planning: "🗓",
  active: "🟢",
  completed: "✅",
  archived: "📦",
};

export function TripList() {
  const nav = useNavigation();
  const trips = useAsync(() => api.trips.list(), []);
  const [creating, setCreating] = useState(false);

  return (
    <Screen
      title="Your trips"
      action={
        <Button small onClick={() => setCreating(true)}>
          New
        </Button>
      }
    >
      <Loaded state={trips}>
        {(list) =>
          list.length === 0 ? (
            <Empty
              emoji="🧳"
              title="No trips yet"
              description="A trip holds everything: who is going, the timeline, the cars, the beds and the money."
              action={<Button onClick={() => setCreating(true)}>Create a trip</Button>}
            />
          ) : (
            <div className="stack">
              {list.map((trip) => (
                <TripRow key={trip.id} trip={trip} onOpen={() => nav.push({ name: "trip", params: { tripId: trip.id } })} />
              ))}
            </div>
          )
        }
      </Loaded>

      {creating && (
        <CreateTripSheet
          onClose={() => setCreating(false)}
          onCreated={(id) => {
            setCreating(false);
            trips.reload();
            nav.push({ name: "trip", params: { tripId: id } });
          }}
        />
      )}
    </Screen>
  );
}

function TripRow({ trip, onOpen }: { trip: TripSummary; onOpen: () => void }) {
  return (
    <Card onClick={onOpen}>
      <div className="card-row">
        <div>
          <div className="title">
            {STATUS_ICONS[trip.status]} {trip.title}
          </div>
          <div className="muted">{dateRange(trip.start_date, trip.end_date)}</div>
        </div>
        {trip.open_decisions > 0 && <span className="badge">{trip.open_decisions} 🗳</span>}
      </div>
      <div className="muted">{trip.member_count} going</div>
    </Card>
  );
}

/** Two required answers — a name and dates — and sensible defaults for the rest. */
function CreateTripSheet({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (tripId: string) => void;
}) {
  const today = new Date().toISOString().slice(0, 10);
  const [title, setTitle] = useState("");
  const [start, setStart] = useState(today);
  const [end, setEnd] = useState(today);
  const [currency, setCurrency] = useState("EUR");
  const [error, setError] = useState<ApiError | undefined>();

  // The phone's timezone is almost always the trip's; it stays editable in
  // settings for the case where it is not.
  const timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

  const submit = async () => {
    setError(undefined);
    try {
      const trip = await api.trips.create({
        title,
        start_date: start,
        end_date: end < start ? start : end,
        timezone,
        currency,
      });
      onCreated(trip.id);
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="New trip" onClose={onClose}>
      <Field label="What is it called?" error={error?.fields.title}>
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Brevet Łódź 200"
          autoFocus
        />
      </Field>
      <div className="row">
        <Field label="From">
          <input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
        </Field>
        <Field label="To">
          <input type="date" value={end} min={start} onChange={(e) => setEnd(e.target.value)} />
        </Field>
      </div>
      <Field label="Currency" error={error?.fields.currency}>
        <select value={currency} onChange={(e) => setCurrency(e.target.value)}>
          {CURRENCIES.map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </select>
      </Field>
      <p className="tiny">Times will be shown in {timezone}. You can change that later.</p>
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={title.trim().length < 2}>
        Create trip
      </AsyncButton>
    </Sheet>
  );
}
