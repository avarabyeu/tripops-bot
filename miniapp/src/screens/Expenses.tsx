import { useState } from "react";
import { api, ApiError } from "../api";
import { CATEGORY_ICONS, dayIn, money, parseMoney } from "../format";
import { useNavigation, useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Chip, Empty, Field, Sheet } from "../components/ui";
import type { ExpenseCategory, Member } from "../types";

const CATEGORIES: ExpenseCategory[] = [
  "fuel",
  "accommodation",
  "food",
  "transport",
  "parking",
  "registration",
  "equipment",
  "other",
];

/** What the group spent, and who paid for it. */
export function Expenses() {
  const tripId = useParam("tripId");
  const nav = useNavigation();
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const expenses = useAsync(() => api.expenses.list(tripId), [tripId]);
  const members = useAsync(() => api.members.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);

  const currency = trip.data?.trip.currency ?? "EUR";
  const timezone = trip.data?.trip.timezone ?? "UTC";
  const total = (expenses.data ?? []).reduce((sum, e) => sum + e.amount_minor, 0);

  return (
    <Screen
      title="Expenses"
      subtitle={expenses.data && expenses.data.length > 0 ? `${money(total, currency)} total` : undefined}
      action={
        <Button small onClick={() => setAdding(true)}>
          Add
        </Button>
      }
    >
      <Loaded state={expenses}>
        {(list) =>
          list.length === 0 ? (
            <Empty
              emoji="💰"
              title="Nothing recorded yet"
              description="Add what people paid for and TripOps works out who owes whom. It records money; it never moves it."
              action={<Button onClick={() => setAdding(true)}>Add an expense</Button>}
            />
          ) : (
            <div className="stack tight">
              {list.map((expense) => (
                <Card key={expense.id} tight>
                  <div className="card-row">
                    <div>
                      <div className="title">
                        {CATEGORY_ICONS[expense.category]} {expense.title}
                      </div>
                      <div className="tiny">
                        paid by {expense.paid_by_name} · {dayIn(expense.spent_at, timezone)} ·{" "}
                        {expense.participants.length} people
                      </div>
                    </div>
                    <div className="title mono-num">{money(expense.amount_minor, expense.currency)}</div>
                  </div>
                </Card>
              ))}
            </div>
          )
        }
      </Loaded>

      <div className="bottom-action">
        <Button block variant="secondary" onClick={() => nav.push({ name: "balances", params: { tripId } })}>
          ⚖️ Who owes whom
        </Button>
      </div>

      {adding && members.data && trip.data && (
        <AddExpenseSheet
          tripId={tripId}
          currency={currency}
          members={members.data.filter((m) => m.status === "active")}
          defaultPayer={trip.data.me.id}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            expenses.reload();
          }}
        />
      )}
    </Screen>
  );
}

/**
 * Adding an expense is the most-used form in the app, so it defaults
 * aggressively: you paid, everyone splits it equally, today.
 */
function AddExpenseSheet({
  tripId,
  currency,
  members,
  defaultPayer,
  onClose,
  onCreated,
}: {
  tripId: string;
  currency: string;
  members: Member[];
  defaultPayer: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [title, setTitle] = useState("");
  const [amount, setAmount] = useState("");
  const [category, setCategory] = useState<ExpenseCategory>("fuel");
  const [payer, setPayer] = useState(defaultPayer);
  const [participants, setParticipants] = useState<string[]>(members.map((m) => m.id));
  const [error, setError] = useState<ApiError | undefined>();

  const minor = parseMoney(amount);
  const valid = title.trim().length > 0 && Number.isFinite(minor) && minor > 0 && participants.length > 0;
  const each = participants.length > 0 && Number.isFinite(minor) ? Math.floor(minor / participants.length) : 0;

  const toggle = (id: string) =>
    setParticipants((c) => (c.includes(id) ? c.filter((p) => p !== id) : [...c, id]));

  const submit = async () => {
    setError(undefined);
    try {
      await api.expenses.create(tripId, {
        title,
        amount_minor: minor,
        category,
        paid_by: payer,
        split_type: "equal",
        participants: participants.map((id) => ({ member_id: id })),
      });
      onCreated();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="Add an expense" onClose={onClose}>
      <Field label="What for?" error={error?.fields.title}>
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Fuel" autoFocus />
      </Field>
      <Field label={`How much (${currency})`} error={error?.fields.amount_minor}>
        <input
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          inputMode="decimal"
          placeholder="120.00"
        />
      </Field>
      <Field label="Category">
        <div className="row wrap">
          {CATEGORIES.map((c) => (
            <Chip key={c} active={category === c} onClick={() => setCategory(c)}>
              {CATEGORY_ICONS[c]} {c}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="Who paid?" error={error?.fields.paid_by}>
        <div className="row wrap">
          {members.map((m) => (
            <Chip key={m.id} active={payer === m.id} onClick={() => setPayer(m.id)}>
              {m.display_name}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="Split between">
        <div className="row wrap">
          {members.map((m) => (
            <Chip key={m.id} active={participants.includes(m.id)} onClick={() => toggle(m.id)}>
              {m.display_name}
            </Chip>
          ))}
        </div>
      </Field>
      {each > 0 && (
        <p className="tiny">
          {money(each, currency)} each. Leftover cents go to the first people on the list, so the
          shares always add back up to the total.
        </p>
      )}
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={!valid}>
        Add expense
      </AsyncButton>
    </Sheet>
  );
}
