import { useState } from "react";
import { api } from "../api";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Empty, Field, ProgressBar, Sheet } from "../components/ui";
import { haptic } from "../telegram";
import type { Checklist } from "../types";

/** Shared and personal packing lists. */
export function Checklists() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const lists = useAsync(() => api.checklists.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);

  const canManage = trip.data ? trip.data.me.role !== "member" : false;

  // Ticking a box is the one action worth making optimistic: it is a toggle,
  // it cannot be refused, and waiting for a round trip makes the list feel dead.
  const toggle = async (list: Checklist, itemId: string, completed: boolean) => {
    haptic.tap();
    lists.set((current) => ({
      ...current,
      checklists: current.checklists.map((l) =>
        l.id !== list.id
          ? l
          : {
              ...l,
              items: l.items.map((i) => (i.id === itemId ? { ...i, completed } : i)),
              progress: {
                total: l.progress.total,
                completed: l.progress.completed + (completed ? 1 : -1),
              },
            },
      ),
      progress: {
        total: current.progress.total,
        completed: current.progress.completed + (completed ? 1 : -1),
      },
    }));
    try {
      await api.checklists.toggleItem(tripId, itemId, completed);
    } catch {
      haptic.error();
      lists.reload();
    }
  };

  return (
    <Screen
      title="Checklist"
      subtitle={
        lists.data && lists.data.progress.total > 0
          ? `${lists.data.progress.completed} / ${lists.data.progress.total} completed`
          : undefined
      }
      action={
        <Button small onClick={() => setAdding(true)}>
          New list
        </Button>
      }
    >
      <Loaded state={lists}>
        {(data) =>
          data.checklists.length === 0 ? (
            <Empty
              emoji="🎒"
              title="No checklists yet"
              description="A shared list means nobody arrives without a rear light. A personal one is yours alone."
              action={<Button onClick={() => setAdding(true)}>Create a list</Button>}
            />
          ) : (
            <div className="stack">
              {data.checklists.map((list) => (
                <ListCard
                  key={list.id}
                  list={list}
                  onToggle={(itemId, completed) => void toggle(list, itemId, completed)}
                  onAddItem={async (title) => {
                    await api.checklists.addItem(tripId, list.id, title);
                    lists.reload();
                  }}
                />
              ))}
            </div>
          )
        }
      </Loaded>

      {adding && (
        <NewListSheet
          tripId={tripId}
          canCreateShared={canManage}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            lists.reload();
          }}
        />
      )}
    </Screen>
  );
}

function ListCard({
  list,
  onToggle,
  onAddItem,
}: {
  list: Checklist;
  onToggle: (itemId: string, completed: boolean) => void;
  onAddItem: (title: string) => Promise<void>;
}) {
  const [draft, setDraft] = useState("");

  return (
    <Card>
      <div className="card-row">
        <div className="title">
          {list.scope === "personal" ? "🔒 " : ""}
          {list.title}
        </div>
        <span className="tiny mono-num">
          {list.progress.completed} / {list.progress.total}
        </span>
      </div>
      <ProgressBar completed={list.progress.completed} total={list.progress.total} />

      <div className="stack tight">
        {list.items.map((item) => (
          <label key={item.id} className="row" style={{ cursor: "pointer" }}>
            <input
              type="checkbox"
              checked={item.completed}
              onChange={(e) => onToggle(item.id, e.target.checked)}
              style={{ width: 22, height: 22, minHeight: 22, flex: "none" }}
            />
            <span className={item.completed ? "strike" : ""}>{item.title}</span>
            {item.assigned_to_name && <span className="tiny">({item.assigned_to_name})</span>}
          </label>
        ))}
      </div>

      <form
        className="row"
        onSubmit={(e) => {
          e.preventDefault();
          const title = draft.trim();
          if (!title) return;
          setDraft("");
          void onAddItem(title);
        }}
      >
        <input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="Add an item" />
        <Button type="submit" small disabled={draft.trim().length === 0}>
          Add
        </Button>
      </form>
    </Card>
  );
}

function NewListSheet({
  tripId,
  canCreateShared,
  onClose,
  onCreated,
}: {
  tripId: string;
  canCreateShared: boolean;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [title, setTitle] = useState("");
  const [scope, setScope] = useState<"shared" | "personal">(canCreateShared ? "shared" : "personal");
  const [items, setItems] = useState("");
  const [error, setError] = useState<string | undefined>();

  const submit = async () => {
    setError(undefined);
    try {
      await api.checklists.create(tripId, {
        title,
        scope,
        items: items
          .split("\n")
          .map((i) => i.trim())
          .filter(Boolean),
      });
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create the list");
    }
  };

  return (
    <Sheet title="New checklist" onClose={onClose}>
      <Field label="Title">
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="🚴 Bike" autoFocus />
      </Field>
      {canCreateShared && (
        <Field label="Who is it for?">
          <div className="row">
            <Button
              small
              variant={scope === "shared" ? "primary" : "secondary"}
              onClick={() => setScope("shared")}
            >
              Everyone
            </Button>
            <Button
              small
              variant={scope === "personal" ? "primary" : "secondary"}
              onClick={() => setScope("personal")}
            >
              Just me
            </Button>
          </div>
        </Field>
      )}
      <Field label="Items, one per line">
        <textarea
          rows={5}
          value={items}
          onChange={(e) => setItems(e.target.value)}
          placeholder={"Pump\nSpare tubes\nFront light"}
        />
      </Field>
      {error && <span className="field-error">{error}</span>}
      <AsyncButton block onClick={submit} disabled={title.trim().length < 2}>
        Create list
      </AsyncButton>
    </Sheet>
  );
}
