import { api } from "../api";
import { money, signedMoney } from "../format";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Card, Empty } from "../components/ui";
import { confirm, haptic } from "../telegram";

/**
 * Who owes whom, and the shortest set of payments that clears it.
 *
 * "Mark as settled" records that money changed hands. TripOps never moves it,
 * and the copy says so, because a button next to an amount invites the
 * assumption that it does.
 */
export function Balances() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const report = useAsync(() => api.expenses.balances(tripId), [tripId]);
  const meId = trip.data?.me.id ?? "";

  return (
    <Screen
      title="Balance"
      subtitle={report.data ? `${money(report.data.total_minor, report.data.currency)} spent in total` : undefined}
    >
      <Loaded state={report}>
        {(data) => {
          const settled = data.balances.every((b) => b.balance_minor === 0);
          if (data.balances.length === 0 || (settled && data.total_minor === 0)) {
            return (
              <Empty
                emoji="⚖️"
                title="Nothing to settle"
                description="Once somebody records an expense, the split and the transfers show up here."
              />
            );
          }

          return (
            <>
              <Card>
                {data.balances.map((balance) => (
                  <div key={balance.member_id} className="card-row">
                    <span>{balance.display_name}</span>
                    <span
                      className={`mono-num ${
                        balance.balance_minor > 0 ? "positive" : balance.balance_minor < 0 ? "negative" : "muted"
                      }`}
                    >
                      {balance.balance_minor === 0
                        ? "settled"
                        : signedMoney(balance.balance_minor, data.currency)}
                    </span>
                  </div>
                ))}
              </Card>

              {data.transfers.length === 0 ? (
                <Card tight>
                  <div className="row">
                    <span>🎉</span>
                    <span className="muted">Everything is settled.</span>
                  </div>
                </Card>
              ) : (
                <>
                  <div className="section-label">Suggested transfers</div>
                  <div className="stack tight">
                    {data.transfers.map((transfer, i) => {
                      const involved = transfer.from_member_id === meId || transfer.to_member_id === meId;
                      return (
                        <Card key={`${transfer.from_member_id}-${transfer.to_member_id}-${i}`} tight>
                          <div className="card-row">
                            <span>
                              <strong>{transfer.from_name}</strong> owes{" "}
                              <strong>{transfer.to_name}</strong>
                            </span>
                            <span className="title mono-num">
                              {money(transfer.amount_minor, transfer.currency)}
                            </span>
                          </div>
                          {involved && (
                            <AsyncButton
                              small
                              onClick={async () => {
                                const ok = await confirm(
                                  `Mark ${money(transfer.amount_minor, transfer.currency)} from ${
                                    transfer.from_name
                                  } to ${transfer.to_name} as paid?`,
                                );
                                if (!ok) return;
                                await api.expenses.settle(tripId, {
                                  from_member_id: transfer.from_member_id,
                                  to_member_id: transfer.to_member_id,
                                  amount_minor: transfer.amount_minor,
                                });
                                haptic.success();
                                report.reload();
                              }}
                            >
                              ✅ Mark as settled
                            </AsyncButton>
                          )}
                        </Card>
                      );
                    })}
                  </div>
                  <p className="tiny">
                    TripOps does not move money. Pay however you normally do, then mark it here.
                  </p>
                </>
              )}

              {data.settlements.length > 0 && (
                <>
                  <div className="section-label">Already paid</div>
                  <Card tight>
                    {data.settlements.map((s) => (
                      <div key={s.id} className="card-row tiny">
                        <span>
                          {s.from_name} → {s.to_name}
                        </span>
                        <span className="mono-num">
                          {money(s.amount_minor, s.currency)} {s.status === "pending" ? "· planned" : "✓"}
                        </span>
                      </div>
                    ))}
                  </Card>
                </>
              )}
            </>
          );
        }}
      </Loaded>
    </Screen>
  );
}
