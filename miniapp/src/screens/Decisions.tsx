import { useState } from "react";
import { api, ApiError } from "../api";
import { dateTimeIn, relative } from "../format";
import { useParam } from "../router";
import { useAsync } from "../useAsync";
import { Loaded, Screen } from "../components/Screen";
import { AsyncButton, Button, Card, Empty, Field, ProgressBar, Sheet } from "../components/ui";
import type { Decision } from "../types";

/**
 * Group decisions.
 *
 * Voting advises; it never decides. An organiser resolves the decision
 * explicitly, and the UI keeps that distinction visible.
 */
export function Decisions() {
  const tripId = useParam("tripId");
  const trip = useAsync(() => api.trips.get(tripId), [tripId]);
  const decisions = useAsync(() => api.decisions.list(tripId), [tripId]);
  const [adding, setAdding] = useState(false);

  const canManage = trip.data ? trip.data.me.role !== "member" : false;
  const timezone = trip.data?.trip.timezone ?? "UTC";

  const replace = (updated: Decision) =>
    decisions.set((c) => c.map((d) => (d.id === updated.id ? updated : d)));

  return (
    <Screen
      title="Decisions"
      action={
        canManage ? (
          <Button small onClick={() => setAdding(true)}>
            Ask
          </Button>
        ) : undefined
      }
    >
      <Loaded state={decisions}>
        {(list) =>
          list.length === 0 ? (
            <Empty
              emoji="🗳"
              title="No decisions yet"
              description="Use a decision to let the group choose:"
              bullets={["departure time", "accommodation", "route", "restaurant"]}
              action={canManage ? <Button onClick={() => setAdding(true)}>Create a decision</Button> : undefined}
            />
          ) : (
            <div className="stack">
              {list
                .filter((d) => d.status !== "cancelled")
                .map((decision) => (
                  <DecisionCard
                    key={decision.id}
                    decision={decision}
                    timezone={timezone}
                    canManage={canManage}
                    onVote={async (optionId) => replace(await api.decisions.vote(tripId, decision.id, optionId))}
                    onClose={async () => replace(await api.decisions.close(tripId, decision.id))}
                    onResolve={async (optionId) =>
                      replace(await api.decisions.resolve(tripId, decision.id, optionId))
                    }
                  />
                ))}
            </div>
          )
        }
      </Loaded>

      {adding && (
        <AskSheet
          tripId={tripId}
          onClose={() => setAdding(false)}
          onCreated={() => {
            setAdding(false);
            decisions.reload();
          }}
        />
      )}
    </Screen>
  );
}

function DecisionCard({
  decision,
  timezone,
  canManage,
  onVote,
  onClose,
  onResolve,
}: {
  decision: Decision;
  timezone: string;
  canManage: boolean;
  onVote: (optionId: string) => Promise<void>;
  onClose: () => Promise<void>;
  onResolve: (optionId: string) => Promise<void>;
}) {
  const resolved = decision.options.find((o) => o.id === decision.resolved_option_id);
  const open = decision.status === "open";

  return (
    <Card>
      <div className="card-row">
        <div className="title">{decision.title}</div>
        {decision.status === "resolved" && <span>✅</span>}
      </div>
      {decision.description && <div className="muted">{decision.description}</div>}

      {resolved ? (
        <div className="stack tight">
          <div className="muted">
            Decided: <strong>{resolved.label}</strong>
          </div>
          {decision.resolution_note && <div className="tiny">{decision.resolution_note}</div>}
        </div>
      ) : (
        <div className="stack tight">
          {decision.options.map((option) => {
            const mine = decision.my_vote === option.id;
            const share = decision.total_votes === 0 ? 0 : option.votes;
            return (
              <div
                key={option.id}
                role={open ? "button" : undefined}
                tabIndex={open ? 0 : undefined}
                onClick={open ? () => void onVote(option.id) : undefined}
                onKeyDown={open ? (e) => e.key === "Enter" && void onVote(option.id) : undefined}
                style={{ cursor: open ? "pointer" : "default" }}
              >
                <div className="card-row">
                  <span>
                    {mine ? "🔘" : "⚪"} {option.label}
                  </span>
                  <span className="tiny mono-num">{option.votes}</span>
                </div>
                <ProgressBar completed={share} total={Math.max(decision.eligible, 1)} />
                {option.voters.length > 0 && (
                  <div className="tiny">{option.voters.map((v) => v.display_name).join(", ")}</div>
                )}
              </div>
            );
          })}
        </div>
      )}

      <div className="card-row">
        <span className="tiny">
          {decision.total_votes} of {decision.eligible} voted
          {decision.pending.length > 0 &&
            decision.pending.length <= 3 &&
            ` · waiting for ${decision.pending.map((p) => p.display_name).join(", ")}`}
        </span>
        {open && decision.deadline && (
          <span className="tiny" title={dateTimeIn(decision.deadline, timezone)}>
            closes {relative(decision.deadline)}
          </span>
        )}
      </div>

      {canManage && open && (
        <AsyncButton small variant="secondary" onClick={onClose}>
          Close voting
        </AsyncButton>
      )}
      {canManage && decision.status === "closed" && (
        <div className="stack tight">
          <div className="tiny">Voting is closed. Pick the outcome so everyone sees it.</div>
          <div className="row wrap">
            {decision.options.map((option) => (
              <AsyncButton key={option.id} small onClick={() => onResolve(option.id)}>
                {option.label}
              </AsyncButton>
            ))}
          </div>
        </div>
      )}
    </Card>
  );
}

function AskSheet({
  tripId,
  onClose,
  onCreated,
}: {
  tripId: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [title, setTitle] = useState("");
  const [options, setOptions] = useState(["", ""]);
  const [deadline, setDeadline] = useState("");
  const [error, setError] = useState<ApiError | undefined>();

  const setOption = (index: number, value: string) =>
    setOptions((current) => current.map((o, i) => (i === index ? value : o)));

  const filled = options.map((o) => o.trim()).filter(Boolean);

  const submit = async () => {
    setError(undefined);
    try {
      await api.decisions.create(tripId, {
        title,
        options: filled,
        ...(deadline ? { deadline: new Date(deadline).toISOString() } : {}),
      });
      onCreated();
    } catch (err) {
      if (err instanceof ApiError) setError(err);
      else throw err;
    }
  };

  return (
    <Sheet title="Ask the group" onClose={onClose}>
      <Field label="Question" error={error?.fields.title}>
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Where should we stay?"
          autoFocus
        />
      </Field>
      <Field label="Options" error={error?.fields.options}>
        <div className="stack tight">
          {options.map((option, i) => (
            <input
              key={i}
              value={option}
              onChange={(e) => setOption(i, e.target.value)}
              placeholder={`Option ${i + 1}`}
            />
          ))}
          {options.length < 10 && (
            <Button variant="ghost" small onClick={() => setOptions([...options, ""])}>
              + Add option
            </Button>
          )}
        </div>
      </Field>
      <Field label="Voting closes (optional)" error={error?.fields.deadline}>
        <input type="datetime-local" value={deadline} onChange={(e) => setDeadline(e.target.value)} />
      </Field>
      <p className="tiny">
        At the deadline voting closes by itself. Nothing is applied automatically — you confirm the
        outcome.
      </p>
      {error && !Object.keys(error.fields).length && <span className="field-error">{error.message}</span>}
      <AsyncButton block onClick={submit} disabled={title.trim().length < 3 || filled.length < 2}>
        Ask the group
      </AsyncButton>
    </Sheet>
  );
}
