import { useState } from "react";
import { api, ApiError } from "../api";
import { VEHICLE_ICONS, VEHICLE_TYPE_NAMES } from "../format";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Chip, Empty, Field, Sheet } from "../components/ui";
import { useSuggestedName } from "../useSuggestedName";
import type { Member, Vehicle } from "../types";

type VehicleType = Vehicle["type"];

// Ordered by how often a group needs them.
const VEHICLE_TYPES: VehicleType[] = ["car", "van", "train", "bus", "other"];

/** Who is driving, who is riding, and whether everyone has a seat. */
export function Logistics() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const vehicles = useAsync(() => api.vehicles.list(tripId), [tripId]);
  const members = useAsync(() => api.members.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);

  const canManage = trip.data ? trip.data.me.role !== "member" : false;
  const meId = trip.data?.me.id ?? "";

  const seated = new Set<string>();
  for (const v of vehicles.data ?? []) {
    if (v.driver_member_id) seated.add(v.driver_member_id);
    for (const p of v.passengers) seated.add(p.member_id);
  }
  const unseated = (members.data ?? []).filter((m) => m.status === "active" && !seated.has(m.id));

  return (
    <Screen
      title="Logistics"
      action={
        canManage ? (
          <Button small onClick={() => setAdding(true)}>
            Add
          </Button>
        ) : undefined
      }
    >
      <Loaded state={vehicles}>
        {(list) =>
          list.length === 0 ? (
            <Empty
              emoji="🚗"
              title="No vehicles yet"
              description="Add the cars and who drives them so nobody is left without a seat."
              action={canManage ? <Button onClick={() => setAdding(true)}>Add a vehicle</Button> : undefined}
            />
          ) : (
            <div className="stack">
              {list.map((vehicle) => (
                <VehicleCard
                  key={vehicle.id}
                  vehicle={vehicle}
                  meId={meId}
                  onJoin={async () => {
                    const updated = await api.vehicles.join(tripId, vehicle.id);
                    vehicles.set((c) => c.map((v) => (v.id === updated.id ? updated : v)));
                  }}
                  onLeave={async () => {
                    const updated = await api.vehicles.leave(tripId, vehicle.id);
                    vehicles.set((c) => c.map((v) => (v.id === updated.id ? updated : v)));
                  }}
                />
              ))}
              {unseated.length > 0 && (
                <Card tight>
                  <div className="muted">
                    Without a seat: {unseated.map((m) => m.display_name).join(", ")}
                  </div>
                </Card>
              )}
            </div>
          )
        }
      </Loaded>

      {adding && members.data && (
        <AddVehicleSheet
          tripId={tripId}
          existing={vehicles.data ?? []}
          members={members.data.filter((m) => m.status === "active")}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            vehicles.reload();
          }}
        />
      )}
    </Screen>
  );
}

function VehicleCard({
  vehicle,
  meId,
  onJoin,
  onLeave,
}: {
  vehicle: Vehicle;
  meId: string;
  onJoin: () => Promise<void>;
  onLeave: () => Promise<void>;
}) {
  const riding =
    vehicle.driver_member_id === meId || vehicle.passengers.some((p) => p.member_id === meId);
  const driving = vehicle.driver_member_id === meId;
  const full = vehicle.seats_left <= 0;

  return (
    <Card>
      <div className="card-row">
        <div className="title">
          {VEHICLE_ICONS[vehicle.type]} {vehicle.name}
        </div>
        <span className={`muted mono-num${vehicle.seats_used > vehicle.capacity ? " negative" : ""}`}>
          {vehicle.seats_used} / {vehicle.capacity}
        </span>
      </div>
      {vehicle.driver_name && <div className="muted">Driver: {vehicle.driver_name}</div>}
      {vehicle.passengers.length > 0 && (
        <div className="muted">
          {vehicle.passengers
            .filter((p) => p.member_id !== vehicle.driver_member_id)
            .map((p) => p.display_name)
            .join(", ")}
        </div>
      )}
      {vehicle.notes && <div className="tiny">{vehicle.notes}</div>}

      {/* Driving is an organiser's assignment; riding is the passenger's own. */}
      {!driving &&
        (riding ? (
          <AsyncButton small variant="secondary" onClick={onLeave}>
            Leave this car
          </AsyncButton>
        ) : (
          <AsyncButton small onClick={onJoin} disabled={full}>
            {full ? "Full" : "Take a seat"}
          </AsyncButton>
        ))}
    </Card>
  );
}

function AddVehicleSheet({
  tripId,
  existing,
  members,
  onClose,
  onCreated,
}: {
  tripId: string;
  existing: Vehicle[];
  members: Member[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const [type, setType] = useState<VehicleType>("car");
  // "Car 1", then "Car 2": the number continues from what the trip already has,
  // so adding a second car needs no typing either.
  const suggestFor = (kind: VehicleType): string => {
    const label = VEHICLE_TYPE_NAMES[kind];
    if (!label) return "";
    return `${label} ${existing.filter((v) => v.type === kind).length + 1}`;
  };
  const name = useSuggestedName(suggestFor("car"));
  const [capacity, setCapacity] = useState(4);
  const [driver, setDriver] = useState("");
  const [passengers, setPassengers] = useState<string[]>([]);
  const [error, setError] = useState<ApiError | undefined>();

  const toggle = (id: string) =>
    setPassengers((current) =>
      current.includes(id) ? current.filter((p) => p !== id) : [...current, id],
    );

  const chooseType = (next: VehicleType) => {
    setType(next);
    name.suggest(suggestFor(next));
  };

  // The driver takes a seat, so the count shown here matches the server's rule.
  const taken = new Set(passengers.concat(driver ? [driver] : [])).size;

  const submit = async () => {
    setError(undefined);
    try {
      await api.vehicles.create(tripId, {
        name: name.value,
        type,
        capacity,
        ...(driver ? { driver_member_id: driver } : {}),
        passenger_ids: passengers.filter((p) => p !== driver),
      });
      onCreated();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="Add a vehicle" onClose={onClose}>
      {/* Kind first, so the name below is usually already right. */}
      <Field label="Kind">
        <div className="row wrap">
          {VEHICLE_TYPES.map((t) => (
            <Chip key={t} active={type === t} onClick={() => chooseType(t)}>
              {VEHICLE_ICONS[t]} {VEHICLE_TYPE_NAMES[t] || "Other"}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="Name" error={error?.fields.name}>
        <input value={name.value} onChange={(e) => name.edit(e.target.value)} placeholder="Car 1" />
      </Field>
      <Field label="Seats (including the driver)" error={error?.fields.capacity}>
        <input
          type="number"
          min={1}
          max={20}
          value={capacity}
          onChange={(e) => setCapacity(Number(e.target.value))}
        />
      </Field>
      <Field label="Driver">
        <div className="row wrap">
          {members.map((m) => (
            <Chip key={m.id} active={driver === m.id} onClick={() => setDriver(driver === m.id ? "" : m.id)}>
              {m.display_name}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label={`Passengers — ${taken} of ${capacity} seats`}>
        <div className="row wrap">
          {members
            .filter((m) => m.id !== driver)
            .map((m) => (
              <Chip key={m.id} active={passengers.includes(m.id)} onClick={() => toggle(m.id)}>
                {m.display_name}
              </Chip>
            ))}
        </div>
      </Field>
      {error && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={name.value.trim().length < 1 || taken > capacity}>
        {taken > capacity ? "Too many people" : "Add vehicle"}
      </AsyncButton>
    </Sheet>
  );
}
