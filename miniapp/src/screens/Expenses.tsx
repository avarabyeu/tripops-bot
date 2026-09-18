import { useState } from "react";
import { api, ApiError } from "../api";
import { CATEGORY_ICONS, CATEGORY_NAMES, dayIn, money, parseMoney } from "../format";
import { useNavigation, useParam } from "../router";
import { confirm } from "../telegram";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Chip, Empty, Field, Sheet } from "../components/ui";
import { useSuggestedName } from "../useSuggestedName";
import type { Expense, ExpenseCategory, Member } from "../types";

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
  const [editing, setEditing] = useState<Expense | undefined>();

  const currency = trip.data?.trip.currency ?? "EUR";
  const timezone = trip.data?.trip.timezone ?? "UTC";
  const total = (expenses.data ?? []).reduce((sum, e) => sum + e.amount_minor, 0);
  const active = (members.data ?? []).filter((m) => m.status === "active");

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
                // Tapping an expense opens it for editing. Splits go stale —
                // somebody joins the trip after the room is booked — so this
                // is a normal thing to do, not a buried correction.
                <Card key={expense.id} tight onClick={() => setEditing(expense)}>
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
        <div className="pair">
          <Button
            block
            variant="secondary"
            onClick={() => nav.push({ name: "expense-report", params: { tripId } })}
          >
            📊 Report
          </Button>
          <Button block variant="secondary" onClick={() => nav.push({ name: "balances", params: { tripId } })}>
            ⚖️ Who owes whom
          </Button>
        </div>
      </div>

      {adding && members.data && trip.data && (
        <ExpenseSheet
          tripId={tripId}
          currency={currency}
          members={active}
          defaultPayer={trip.data.me.id}
          onClose={() => setAdding(false)}
          onDone={() => {
            setAdding(false);
            expenses.reload();
          }}
        />
      )}

      {editing && members.data && trip.data && (
        <ExpenseSheet
          tripId={tripId}
          currency={currency}
          members={active}
          defaultPayer={trip.data.me.id}
          expense={editing}
          onClose={() => setEditing(undefined)}
          onDone={() => {
            setEditing(undefined);
            expenses.reload();
          }}
        />
      )}
    </Screen>
  );
}

/**
 * One form for adding and for editing.
 *
 * Adding is the most-used form in the app, so it defaults aggressively: you
 * paid, everyone splits it equally, today. Editing starts from the expense as
 * recorded — including who it was split between, which is the field people
 * come back to change.
 */
function ExpenseSheet({
  tripId,
  currency,
  members,
  defaultPayer,
  expense,
  onClose,
  onDone,
}: {
  tripId: string;
  currency: string;
  members: Member[];
  defaultPayer: string;
  expense?: Expense;
  onClose: () => void;
  onDone: () => void;
}) {
  const editing = expense !== undefined;
  const [amount, setAmount] = useState(expense ? (expense.amount_minor / 100).toFixed(2) : "");
  const [category, setCategory] = useState<ExpenseCategory>(expense?.category ?? "fuel");
  // Picking a category names the expense; most are exactly "Fuel" or "Food".
  // An expense that already has a name keeps it: suggest() only fills a field
  // the user has not made theirs, and editing means they already did.
  const title = useSuggestedName(expense?.title ?? CATEGORY_NAMES.fuel ?? "");
  const [payer, setPayer] = useState(expense?.paid_by ?? defaultPayer);
  const [participants, setParticipants] = useState<string[]>(
    expense ? expense.participants.map((p) => p.member_id) : members.map((m) => m.id),
  );
  const [error, setError] = useState<ApiError | undefined>();

  const minor = parseMoney(amount);
  const valid =
    title.value.trim().length > 0 && Number.isFinite(minor) && minor > 0 && participants.length > 0;
  const each = participants.length > 0 && Number.isFinite(minor) ? Math.floor(minor / participants.length) : 0;

  const toggle = (id: string) =>
    setParticipants((c) => (c.includes(id) ? c.filter((p) => p !== id) : [...c, id]));

  const chooseCategory = (next: ExpenseCategory) => {
    setCategory(next);
    title.suggest(CATEGORY_NAMES[next] ?? "");
  };

  const submit = async () => {
    setError(undefined);
    // Sending the split every time, edit included, is what re-splits a bill
    // between a group that has since grown: the backend replaces the whole
    // participant list and recomputes the shares from it.
    //
    // Always "equal", because that is the only split this form can express.
    // Uneven splits exist in the API and are warned about above rather than
    // silently flattened on save.
    const body = {
      title: title.value,
      amount_minor: minor,
      category,
      paid_by: payer,
      split_type: "equal",
      participants: participants.map((id) => ({ member_id: id })),
    };
    try {
      if (expense) await api.expenses.update(tripId, expense.id, body);
      else await api.expenses.create(tripId, body);
      onDone();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  const remove = async () => {
    if (!expense) return;
    if (!(await confirm(`Delete "${expense.title}"? Everyone's balance changes.`))) return;
    setError(undefined);
    try {
      await api.expenses.remove(tripId, expense.id);
      onDone();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title={editing ? "Edit expense" : "Add an expense"} onClose={onClose}>
      {/* Category first, so the name below is usually already filled in. */}
      <Field label="Category">
        <div className="row wrap">
          {CATEGORIES.map((c) => (
            <Chip key={c} active={category === c} onClick={() => chooseCategory(c)}>
              {CATEGORY_ICONS[c]} {CATEGORY_NAMES[c] || "Other"}
            </Chip>
          ))}
        </div>
      </Field>
      <Field label="What for?" error={error?.fields.title}>
        <input value={title.value} onChange={(e) => title.edit(e.target.value)} placeholder="Fuel" />
      </Field>
      <Field label={`How much (${currency})`} error={error?.fields.amount_minor}>
        <input
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          inputMode="decimal"
          placeholder="120.00"
          autoFocus={!editing}
        />
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
      {editing && expense.split_type !== "equal" && (
        <p className="field-error">
          This expense was split unevenly. Saving here divides it equally instead.
        </p>
      )}
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={!valid}>
        {editing ? "Save changes" : "Add expense"}
      </AsyncButton>
      {editing && (
        <AsyncButton block variant="danger" onClick={remove}>
          Delete expense
        </AsyncButton>
      )}
    </Sheet>
  );
}
