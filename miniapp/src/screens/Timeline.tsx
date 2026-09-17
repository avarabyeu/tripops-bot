import { useState } from "react";
import { api, ApiError } from "../api";
import { dayIn, timeIn, EVENT_ICONS } from "../format";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Chip, Empty, Field, Sheet } from "../components/ui";
import type { EventType, Member, RSVP, TripEvent } from "../types";

const TYPES: { value: EventType; label: string }[] = [
  { value: "departure", label: "🚗 Departure" },
  { value: "arrival", label: "🏁 Arrival" },
  { value: "accommodation", label: "🏠 Check-in" },
  { value: "meal", label: "🍝 Meal" },
  { value: "race", label: "🚴 Race" },
  { value: "activity", label: "🎯 Activity" },
  { value: "transport", label: "🚌 Transport" },
  { value: "custom", label: "📌 Other" },
];

export function Timeline() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const events = useAsync(() => api.events.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);

  const canManage = trip.data ? trip.data.me.role !== "member" : false;
  const timezone = trip.data?.trip.timezone ?? "UTC";
  const meId = trip.data?.me.id ?? "";

  return (
    <Screen
      title="Timeline"
      subtitle={trip.data ? `Times in ${timezone}` : undefined}
      action={
        canManage ? (
          <Button small onClick={() => setAdding(true)}>
            Add
          </Button>
        ) : undefined
      }
    >
      <Loaded state={events}>
        {(list) =>
          list.length === 0 ? (
            <Empty
              emoji="📅"
              title="Nothing scheduled yet"
              description="A timeline answers the question people keep asking in the group chat: when are we leaving?"
              action={canManage ? <Button onClick={() => setAdding(true)}>Add the first event</Button> : undefined}
            />
          ) : (
            <div className="stack">
              {groupByDay(list, timezone).map(([day, dayEvents]) => (
                <div key={day} className="stack tight">
                  <div className="timeline-day">{day}</div>
                  {dayEvents.map((event) => (
                    <EventCard
                      key={event.id}
                      event={event}
                      timezone={timezone}
                      meId={meId}
                      onAnswer={async (status) => {
                        const updated = await api.events.rsvp(tripId, event.id, status);
                        events.set((current) => current.map((e) => (e.id === updated.id ? updated : e)));
                      }}
                    />
                  ))}
                </div>
              ))}
            </div>
          )
        }
      </Loaded>

      {adding && (
        <AddEventSheet
          tripId={tripId}
          timezone={timezone}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            events.reload();
          }}
        />
      )}
    </Screen>
  );
}

/** Groups chronologically ordered events under their day in the trip timezone. */
function groupByDay(events: TripEvent[], timezone: string): [string, TripEvent[]][] {
  const groups = new Map<string, TripEvent[]>();
  for (const event of events) {
    const day = dayIn(event.start_at, timezone);
    const bucket = groups.get(day);
    if (bucket) bucket.push(event);
    else groups.set(day, [event]);
  }
  return [...groups.entries()];
}

const ANSWERS: { value: RSVP; label: string }[] = [
  { value: "attending", label: "☑ In" },
  { value: "not_attending", label: "☐ Out" },
  { value: "maybe", label: "? Maybe" },
];

function EventCard({
  event,
  timezone,
  meId,
  onAnswer,
}: {
  event: TripEvent;
  timezone: string;
  meId: string;
  onAnswer: (status: RSVP) => Promise<void>;
}) {
  const mine = event.participants.find((p) => p.member_id === meId);
  const past = new Date(event.start_at).getTime() < Date.now();

  return (
    <div className="timeline-item">
      <div className="timeline-time">{timeIn(event.start_at, timezone)}</div>
      <Card tight>
        <div className="card-row">
          <div className="title">
            {EVENT_ICONS[event.type]} {event.title}
          </div>
          <span className="tiny mono-num">
            {event.attending}/{event.participants.length}
          </span>
        </div>
        {event.location_name && <div className="muted">📍 {event.location_name}</div>}
        {event.description && <div className="muted">{event.description}</div>}

        <div className="row wrap tiny">
          {event.participants.map((p) => (
            <span key={p.member_id}>
              {p.status === "attending" ? "☑" : p.status === "not_attending" ? "☐" : p.status === "maybe" ? "?" : "·"}{" "}
              {p.display_name}
            </span>
          ))}
        </div>

        {mine && !past && (
          <div className="row wrap">
            {ANSWERS.map((answer) => (
              <Chip
                key={answer.value}
                active={mine.status === answer.value}
                onClick={() => void onAnswer(answer.value)}
              >
                {answer.label}
              </Chip>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

function AddEventSheet({
  tripId,
  timezone,
  onClose,
  onCreated,
}: {
  tripId: string;
  timezone: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [title, setTitle] = useState("");
  const [type, setType] = useState<EventType>("departure");
  const [when, setWhen] = useState(defaultWhen());
  const [location, setLocation] = useState("");
  const [error, setError] = useState<ApiError | undefined>();

  const submit = async () => {
    setError(undefined);
    try {
      await api.events.create(tripId, {
        title,
        type,
        // The picker gives a local wall-clock time; the API takes an instant.
        start_at: new Date(when).toISOString(),
        location_name: location,
      });
      onCreated();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="Add to timeline" onClose={onClose}>
      <Field label="What is happening?" error={error?.fields.title}>
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Departure" autoFocus />
      </Field>
      <Field label="Kind">
        <div className="row wrap">
          {TYPES.map((t) => (
            <Chip key={t.value} active={type === t.value} onClick={() => setType(t.value)}>
              {t.label}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label={`When (${timezone})`} error={error?.fields.start_at}>
        <input type="datetime-local" value={when} onChange={(e) => setWhen(e.target.value)} />
      </Field>
      <Field label="Where (optional)">
        <input value={location} onChange={(e) => setLocation(e.target.value)} placeholder="Piotrkowska 1" />
      </Field>
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <p className="tiny">Everyone on the trip is added, undecided, and can answer for themselves.</p>
      <AsyncButton block onClick={submit} disabled={title.trim().length < 2}>
        Add event
      </AsyncButton>
    </Sheet>
  );
}

/** Tomorrow at 09:00, formatted for datetime-local. */
function defaultWhen(): string {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  d.setHours(9, 0, 0, 0);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export type { Member };
