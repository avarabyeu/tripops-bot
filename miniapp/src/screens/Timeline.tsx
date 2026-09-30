import { useState } from "react";
import { api, ApiError } from "../api";
import {
  dayIn,
  timeIn,
  fromLocalInput,
  dayAheadIn,
  toLocalInput,
  EVENT_ICONS,
  EVENT_TYPE_NAMES,
} from "../format";
import { useParam } from "../router";
import { confirm } from "../telegram";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Chip, Empty, Field, Sheet } from "../components/ui";
import { useSuggestedName } from "../useSuggestedName";
import type { EventType, Member, RSVP, TripEvent } from "../types";

// Ordered by how often a trip needs them, not alphabetically.
const TYPES: EventType[] = [
  "departure",
  "arrival",
  "accommodation",
  "meal",
  "race",
  "activity",
  "transport",
  "custom",
];

/** The chip label; "Other" is the one kind with no name of its own. */
function typeLabel(type: EventType): string {
  return `${EVENT_ICONS[type]} ${EVENT_TYPE_NAMES[type] || "Other"}`;
}

export function Timeline() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const events = useAsync(() => api.events.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<TripEvent | undefined>();

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
                      onEdit={canManage ? () => setEditing(event) : undefined}
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
        <EventSheet
          tripId={tripId}
          timezone={timezone}
          onClose={() => setAdding(false)}
          onDone={() => {
            setAdding(false);
            events.reload();
          }}
        />
      )}

      {editing && (
        <EventSheet
          tripId={tripId}
          timezone={timezone}
          event={editing}
          onClose={() => setEditing(undefined)}
          onDone={() => {
            setEditing(undefined);
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
  onEdit,
  onAnswer,
}: {
  event: TripEvent;
  timezone: string;
  meId: string;
  /** Absent for anyone who may not change the timeline. */
  onEdit?: () => void;
  onAnswer: (status: RSVP) => Promise<void>;
}) {
  const mine = event.participants.find((p) => p.member_id === meId);
  const past = new Date(event.start_at).getTime() < Date.now();

  return (
    <div className="timeline-item">
      <div className="timeline-time">{timeIn(event.start_at, timezone)}</div>
      <Card tight>
        <div className="card-row">
          {onEdit ? (
            <button type="button" className="linklike title" onClick={onEdit}>
              {EVENT_ICONS[event.type]} {event.title}
            </button>
          ) : (
            <div className="title">
              {EVENT_ICONS[event.type]} {event.title}
            </div>
          )}
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

/**
 * One sheet for adding and for editing.
 *
 * Editing exists because plans move: a departure slips an hour, a hotel
 * changes, a leg gets cancelled. Organisers only — the same rule the API
 * enforces with RequireManage.
 */
function EventSheet({
  tripId,
  timezone,
  event,
  onClose,
  onDone,
}: {
  tripId: string;
  timezone: string;
  event?: TripEvent;
  onClose: () => void;
  onDone: () => void;
}) {
  const editing = event !== undefined;
  const [type, setType] = useState<EventType>(event?.type ?? "departure");
  // Pre-filled from the type selected by default, and it follows every change
  // until the field is edited. An event being edited already has its name.
  const title = useSuggestedName(event?.title ?? EVENT_TYPE_NAMES.departure ?? "");
  // Wall clock in the trip's timezone, both ways — never the device's.
  const [when, setWhen] = useState(
    event ? toLocalInput(event.start_at, timezone) : dayAheadIn(timezone, 1, 9),
  );
  const [location, setLocation] = useState(event?.location_name ?? "");
  const [error, setError] = useState<ApiError | undefined>();

  const chooseType = (next: EventType) => {
    setType(next);
    title.suggest(EVENT_TYPE_NAMES[next] ?? "");
  };

  const submit = async () => {
    setError(undefined);
    const body = {
      title: title.value,
      type,
      start_at: fromLocalInput(when, timezone),
      location_name: location,
    };
    try {
      if (event) await api.events.update(tripId, event.id, body);
      else await api.events.create(tripId, body);
      onDone();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  const remove = async () => {
    if (!event) return;
    if (!(await confirm(`Remove "${event.title}" from the timeline?`))) return;
    setError(undefined);
    try {
      await api.events.remove(tripId, event.id);
      onDone();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title={editing ? "Edit event" : "Add to timeline"} onClose={onClose}>
      {/* The kind comes first: picking it names the event, so the field below
          is usually already right. */}
      <Field label="Kind">
        <div className="row wrap">
          {TYPES.map((t) => (
            <Chip key={t} active={type === t} onClick={() => chooseType(t)}>
              {typeLabel(t)}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="What is happening?" error={error?.fields.title}>
        <input
          value={title.value}
          onChange={(e) => title.edit(e.target.value)}
          placeholder="Departure"
        />
      </Field>
      <Field label={`When (${timezone})`} error={error?.fields.start_at}>
        <input type="datetime-local" value={when} onChange={(e) => setWhen(e.target.value)} />
      </Field>
      <Field label="Where (optional)">
        <input value={location} onChange={(e) => setLocation(e.target.value)} placeholder="Piotrkowska 1" />
      </Field>
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      {editing ? (
        <p className="tiny">
          Everyone keeps the answer they already gave. Moving the time re-notifies the group.
        </p>
      ) : (
        <p className="tiny">Everyone on the trip is added, undecided, and can answer for themselves.</p>
      )}
      <AsyncButton block onClick={submit} disabled={title.value.trim().length < 2}>
        {editing ? "Save changes" : "Add event"}
      </AsyncButton>
      {editing && (
        <AsyncButton block variant="danger" onClick={remove}>
          Remove from timeline
        </AsyncButton>
      )}
    </Sheet>
  );
}

export type { Member };
