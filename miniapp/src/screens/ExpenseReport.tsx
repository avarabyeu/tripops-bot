import { api } from "../api";
import { CATEGORY_ICONS, CATEGORY_NAMES, money, signedMoney } from "../format";
import { useNavigation, useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { Button, Card, Empty } from "../components/ui";

/**
 * The trip's spending in one page: the total, where it went, and what each
 * person paid against what their share of everything came to.
 *
 * It answers a different question from the balances screen. That one says what
 * to do next — pay Anna €40. This one says what happened: you put in €150, your
 * share was €120, so you are €30 up. Both come from the same shares, so they
 * cannot disagree.
 */
export function ExpenseReport() {
  const tripId = useParam("tripId");
  const nav = useNavigation();
  const report = useAsync(() => api.expenses.report(tripId), [tripId]);

  return (
    <Screen title="Expense report">
      <Loaded state={report}>
        {(data) =>
          data.count === 0 ? (
            <Empty
              emoji="📊"
              title="Nothing to report yet"
              description="Record what people paid for and the breakdown appears here."
              action={<Button onClick={() => nav.pop()}>Back to expenses</Button>}
            />
          ) : (
            <>
              <Card>
                <div className="card-row">
                  <span className="muted">Total spent</span>
                  <span className="title mono-num">{money(data.total_minor, data.currency)}</span>
                </div>
                <div className="card-row">
                  <span className="muted">Bills recorded</span>
                  <span className="mono-num">{data.count}</span>
                </div>
                <div className="card-row">
                  {/* The average, not what anyone owes — those are different
                      numbers whenever a bill is not split evenly. */}
                  <span className="muted">Average per person</span>
                  <span className="mono-num">{money(data.per_person_minor, data.currency)}</span>
                </div>
              </Card>

              <div className="section-label">Where it went</div>
              <Card tight>
                {data.by_category.map((line) => (
                  <div key={line.category}>
                    <div className="card-row">
                      <span>
                        {CATEGORY_ICONS[line.category]} {CATEGORY_NAMES[line.category] || "Other"}
                        <span className="tiny"> · {line.count}</span>
                      </span>
                      <span className="mono-num">
                        {money(line.total_minor, data.currency)}
                        <span className="tiny"> {line.percent}%</span>
                      </span>
                    </div>
                    <ProportionBar percent={line.percent} />
                  </div>
                ))}
              </Card>

              <div className="section-label">Per person</div>
              <div className="stack tight">
                {data.members.map((member) => (
                  <Card key={member.member_id} tight>
                    <div className="card-row">
                      <div className="title">{member.display_name}</div>
                      <div className={`title mono-num ${balanceClass(member.balance_minor)}`}>
                        {member.balance_minor === 0
                          ? "settled"
                          : signedMoney(member.balance_minor, data.currency)}
                      </div>
                    </div>
                    <div className="card-row">
                      <span className="muted">
                        Paid <span className="tiny">· {member.paid_count} bills</span>
                      </span>
                      <span className="mono-num">{money(member.paid_minor, data.currency)}</span>
                    </div>
                    <div className="card-row">
                      <span className="muted">
                        Their share <span className="tiny">· on {member.share_count} bills</span>
                      </span>
                      <span className="mono-num">{money(member.share_minor, data.currency)}</span>
                    </div>
                    {member.settled_minor !== 0 && (
                      <div className="card-row">
                        <span className="muted">Already settled</span>
                        <span className="mono-num">
                          {signedMoney(member.settled_minor, data.currency)}
                        </span>
                      </div>
                    )}
                    <div className="tiny">{explain(member.balance_minor)}</div>
                  </Card>
                ))}
              </div>

              <div className="bottom-action">
                <Button
                  block
                  variant="secondary"
                  onClick={() => nav.push({ name: "balances", params: { tripId } })}
                >
                  ⚖️ Who owes whom
                </Button>
              </div>
            </>
          )
        }
      </Loaded>
    </Screen>
  );
}

function ProportionBar({ percent }: { percent: number }) {
  // A sliver for anything non-zero: a category that cost something should not
  // render as an empty track.
  return (
    <div className="progress" aria-hidden>
      <span style={{ width: `${Math.max(percent, 2)}%` }} />
    </div>
  );
}

function balanceClass(minor: number): string {
  if (minor > 0) return "positive";
  if (minor < 0) return "negative";
  return "muted";
}

function explain(minor: number): string {
  if (minor > 0) return "The group owes them this.";
  if (minor < 0) return "They owe the group this.";
  return "Square.";
}
