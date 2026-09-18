import { api } from "../api";
import { dateRange, dateTimeIn, money, signedMoney, EVENT_ICONS } from "../format";
import { useNavigation, useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { Card, ProgressBar } from "../components/ui";
import type { AttentionItem, Dashboard } from "../types";

/**
 * The trip dashboard.
 *
 * It answers the nine questions the product promises to answer instantly and
 * puts whatever needs the reader's attention above everything else. It is
 * deliberately not an analytics screen.
 */
export function TripHome() {
  const tripId = useParam("tripId");
  const nav = useNavigation();
  const state = useAsync(() => api.trips.dashboard(tripId), [tripId]);

  return (
    <Loaded state={state} skeletonRows={4}>
      {(data) => (
        <Screen title={data.trip.title} subtitle={dateRange(data.trip.start_date, data.trip.end_date)}>
          <Countdown days={data.days_until_start} />

          {data.attention.length > 0 && (
            <>
              <div className="section-label">Needs attention</div>
              <div className="stack tight">
                {data.attention.slice(0, 5).map((item, i) => (
                  <Attention key={`${item.type}-${i}`} item={item} onOpen={() => openTarget(nav, tripId, item)} />
                ))}
              </div>
            </>
          )}

          {data.next_event && (
            <Card onClick={() => nav.push({ name: "timeline", params: { tripId } })}>
              <div className="tiny">NEXT</div>
              <div className="card-row">
                <div className="title">
                  {EVENT_ICONS[data.next_event.type]} {data.next_event.title}
                </div>
                <div className="muted mono-num">
                  {dateTimeIn(data.next_event.start_at, data.trip.timezone)}
                </div>
              </div>
              {data.next_event.undecided > 0 && (
                <div className="muted">{data.next_event.undecided} still undecided</div>
              )}
            </Card>
          )}

          <div className="menu-grid">
            <Tile emoji="📅" label="Timeline" hint={nextHint(data)} onClick={() => nav.push({ name: "timeline", params: { tripId } })} />
            <Tile
              emoji="👥"
              label="People"
              hint={`${data.people.active} going`}
              onClick={() => nav.push({ name: "people", params: { tripId } })}
            />
            <Tile
              emoji="🚗"
              label="Logistics"
              hint={
                data.transport.vehicles === 0
                  ? "No vehicles"
                  : `${data.transport.seats_used}/${data.transport.seats} seats`
              }
              onClick={() => nav.push({ name: "logistics", params: { tripId } })}
            />
            <Tile
              emoji="🏠"
              label="Stay"
              hint={
                data.accommodation.places === 0
                  ? "Nothing booked"
                  : `${data.accommodation.confirmed}/${data.accommodation.total} confirmed`
              }
              onClick={() => nav.push({ name: "accommodation", params: { tripId } })}
            />
            <Tile
              emoji="💰"
              label="Expenses"
              hint={
                data.expenses.count === 0
                  ? "Nothing yet"
                  : money(data.expenses.total_minor, data.expenses.currency)
              }
              onClick={() => nav.push({ name: "expenses", params: { tripId } })}
            />
            <Tile
              emoji="🗳"
              label="Decisions"
              hint={data.decisions.open === 0 ? "All decided" : `${data.decisions.open} open`}
              badge={data.decisions.awaiting_my_vote || undefined}
              onClick={() => nav.push({ name: "decisions", params: { tripId } })}
            />
            <Tile
              emoji="🎒"
              label="Checklist"
              hint={
                data.checklist.total === 0
                  ? "No lists"
                  : `${data.checklist.completed} / ${data.checklist.total} done`
              }
              onClick={() => nav.push({ name: "checklists", params: { tripId } })}
            />
            <Tile emoji="⚙️" label="Settings" hint={data.me.role} onClick={() => nav.push({ name: "settings", params: { tripId } })} />
          </div>

          {data.checklist.total > 0 && (
            <Card tight>
              <div className="card-row">
                <span className="muted">Packing</span>
                <span className="muted mono-num">
                  {data.checklist.completed} / {data.checklist.total}
                </span>
              </div>
              <ProgressBar completed={data.checklist.completed} total={data.checklist.total} />
            </Card>
          )}

          {data.expenses.my_balance_minor !== 0 && (
            <Card onClick={() => nav.push({ name: "balances", params: { tripId } })}>
              <div className="card-row">
                <span className="muted">
                  {data.expenses.my_balance_minor > 0 ? "You are owed" : "You owe"}
                </span>
                <span
                  className={`title mono-num ${data.expenses.my_balance_minor > 0 ? "positive" : "negative"}`}
                >
                  {signedMoney(data.expenses.my_balance_minor, data.expenses.currency)}
                </span>
              </div>
            </Card>
          )}
        </Screen>
      )}
    </Loaded>
  );
}

function nextHint(data: Dashboard): string {
  if (!data.next_event) return "Nothing planned";
  return dateTimeIn(data.next_event.start_at, data.trip.timezone);
}

function Countdown({ days }: { days: number }) {
  if (days > 0) {
    return <div className="muted">{days === 1 ? "Tomorrow" : `In ${days} days`}</div>;
  }
  if (days === 0) return <div className="muted">Today 🎉</div>;
  return null;
}

function Tile({
  emoji,
  label,
  hint,
  badge,
  onClick,
}: {
  emoji: string;
  label: string;
  hint: string;
  badge?: number;
  onClick: () => void;
}) {
  return (
    <button type="button" className="menu-tile" onClick={onClick}>
      <span className="row" style={{ width: "100%" }}>
        <span className="emoji">{emoji}</span>
        {badge ? <span className="badge">{badge}</span> : null}
      </span>
      <span className="label">{label}</span>
      <span className="tiny">{hint}</span>
    </button>
  );
}

function Attention({ item, onOpen }: { item: AttentionItem; onOpen: () => void }) {
  return (
    <div
      className={`attention ${item.severity}${item.mine ? " mine" : ""}`}
      role="button"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => e.key === "Enter" && onOpen()}
    >
      <span>{icon(item)}</span>
      <div>
        <div className="title" style={{ fontSize: 15 }}>
          {item.title}
        </div>
        {item.description && <div className="tiny">{item.description}</div>}
      </div>
    </div>
  );
}

function icon(item: AttentionItem): string {
  // Money gets its own mark. An unsettled balance is not the same kind of
  // thing as an unconfirmed bed, and a warning triangle over it reads as an
  // error rather than a reminder.
  if (item.type === "trip_balance_outstanding") return "💶";
  if (item.severity === "critical") return "⛔";
  return item.mine ? "👉" : "⚠️";
}

/** Attention items carry where to go, so the list is actionable, not a report. */
function openTarget(
  nav: ReturnType<typeof useNavigation>,
  tripId: string,
  item: AttentionItem,
): void {
  const screen: Record<string, string> = {
    event: "timeline",
    timeline: "timeline",
    decision: "decisions",
    member: "people",
    vehicle: "logistics",
    logistics: "logistics",
    accommodation: "accommodation",
    checklist: "checklists",
    balances: "balances",
  };
  nav.push({ name: screen[item.target.kind] ?? "trip", params: { tripId } });
}
