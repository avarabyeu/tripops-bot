import { useState } from "react";
import { api, ApiError } from "../api";
import { dayIn } from "../format";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Chip, Empty, Field, Sheet } from "../components/ui";
import { openLink } from "../telegram";
import type { Accommodation as Place, Member } from "../types";

/** Where everyone is sleeping, and who has actually confirmed. */
export function Accommodation() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const places = useAsync(() => api.accommodation.list(tripId), [tripId]);
  const members = useAsync(() => api.members.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);

  const canManage = trip.data ? trip.data.me.role !== "member" : false;
  const timezone = trip.data?.trip.timezone ?? "UTC";
  const meId = trip.data?.me.id ?? "";

  return (
    <Screen
      title="Accommodation"
      action={
        canManage ? (
          <Button small onClick={() => setAdding(true)}>
            Add
          </Button>
        ) : undefined
      }
    >
      <Loaded state={places}>
        {(list) =>
          list.length === 0 ? (
            <Empty
              emoji="🏠"
              title="Nothing booked yet"
              description="Add where you are sleeping and who is in which place. Paste the booking link — TripOps keeps it, it does not book anything."
              action={canManage ? <Button onClick={() => setAdding(true)}>Add a place</Button> : undefined}
            />
          ) : (
            <div className="stack">
              {list.map((place) => (
                <PlaceCard
                  key={place.id}
                  place={place}
                  timezone={timezone}
                  meId={meId}
                  onConfirm={async (status) => {
                    const updated = await api.accommodation.setGuestStatus(tripId, place.id, status);
                    places.set((c) => c.map((p) => (p.id === updated.id ? updated : p)));
                  }}
                />
              ))}
            </div>
          )
        }
      </Loaded>

      {adding && members.data && (
        <AddPlaceSheet
          tripId={tripId}
          members={members.data.filter((m) => m.status === "active")}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            places.reload();
          }}
        />
      )}
    </Screen>
  );
}

function PlaceCard({
  place,
  timezone,
  meId,
  onConfirm,
}: {
  place: Place;
  timezone: string;
  meId: string;
  onConfirm: (status: "confirmed" | "declined") => Promise<void>;
}) {
  const mine = place.guests.find((g) => g.member_id === meId);

  return (
    <Card>
      <div className="card-row">
        <div className="title">{place.name}</div>
        {place.capacity > 0 && (
          <span className={`muted mono-num${place.occupied > place.capacity ? " negative" : ""}`}>
            {place.occupied} / {place.capacity}
          </span>
        )}
      </div>
      {place.check_in && place.check_out && (
        <div className="muted">
          {dayIn(place.check_in, timezone)} → {dayIn(place.check_out, timezone)}
        </div>
      )}
      {place.address && <div className="muted">📍 {place.address}</div>}

      <div className="stack tight">
        {place.guests.map((g) => (
          <div key={g.member_id} className="row tiny">
            <span>{g.status === "confirmed" ? "☑" : g.status === "declined" ? "☐" : "·"}</span>
            <span>{g.display_name}</span>
          </div>
        ))}
      </div>

      <div className="row wrap">
        {place.url && (
          <Button small variant="secondary" onClick={() => openLink(place.url!)}>
            🔗 Booking
          </Button>
        )}
        {mine && mine.status !== "confirmed" && (
          <AsyncButton small onClick={() => onConfirm("confirmed")}>
            ☑ Confirm my bed
          </AsyncButton>
        )}
        {mine && mine.status === "confirmed" && (
          <AsyncButton small variant="secondary" onClick={() => onConfirm("declined")}>
            I am not staying here
          </AsyncButton>
        )}
      </div>
    </Card>
  );
}

function AddPlaceSheet({
  tripId,
  members,
  onClose,
  onCreated,
}: {
  tripId: string;
  members: Member[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const [name, setName] = useState("");
  const [address, setAddress] = useState("");
  const [url, setUrl] = useState("");
  const [capacity, setCapacity] = useState(0);
  const [guests, setGuests] = useState<string[]>([]);
  const [error, setError] = useState<ApiError | undefined>();

  const toggle = (id: string) =>
    setGuests((c) => (c.includes(id) ? c.filter((g) => g !== id) : [...c, id]));

  const submit = async () => {
    setError(undefined);
    try {
      await api.accommodation.create(tripId, {
        name,
        address,
        url,
        capacity,
        guest_ids: guests,
      });
      onCreated();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="Add a place" onClose={onClose}>
      <Field label="Name" error={error?.fields.name}>
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Apartment B" autoFocus />
      </Field>
      <Field label="Address (optional)">
        <input value={address} onChange={(e) => setAddress(e.target.value)} />
      </Field>
      <Field label="Booking link (optional)" error={error?.fields.url}>
        <input
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://…"
          inputMode="url"
        />
      </Field>
      <Field label="Beds (0 = do not track)" error={error?.fields.capacity}>
        <input
          type="number"
          min={0}
          max={50}
          value={capacity}
          onChange={(e) => setCapacity(Number(e.target.value))}
        />
      </Field>
      <Field label="Who is staying here?">
        <div className="row wrap">
          {members.map((m) => (
            <Chip key={m.id} active={guests.includes(m.id)} onClick={() => toggle(m.id)}>
              {m.display_name}
            </Chip>
          ))}
        </div>
      </Field>
      <p className="tiny">Guests start as unconfirmed and confirm for themselves.</p>
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={name.trim().length < 1}>
        Add place
      </AsyncButton>
    </Sheet>
  );
}
