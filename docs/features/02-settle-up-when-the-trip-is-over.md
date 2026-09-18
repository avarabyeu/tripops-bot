# Settle up when the trip is over

## The question this answers

"Did anyone ever pay me back for the hotel?"

## Who and when

Everybody, on the Monday after a weekend brevet. The trip is finished, the
group chat has gone quiet, and somebody is out ninety euros.

## Why now

The ledger is the most developed part of the product — splits, balances,
minimal transfers, settlements, and now a report — and the attention engine
does not mention it. All eleven rules in `internal/attention/attention.go` are
about planning: undecided events, pending votes, unconfirmed beds, missing
seats. Every one of them stops being relevant the moment the trip starts.

So the product is attentive right up to departure and then goes silent, which
is exactly when the money question appears. Whoever fronted the accommodation
has to chase people in the chat — the thing TripOps exists to stop.

This is the cheapest genuinely new capability available: one attention rule and
one scheduler reminder over data that is already computed.

## How it works

**An attention rule, `trip_balance_outstanding`.** Once the trip's end date has
passed, a member whose balance is not zero gets an item: *"You owe Anna €40.00"*
or *"The group owes you €90.00"*, targeted at the balances screen. Severity
`warning` for a debt, `info` for a credit — being owed money is not a task.

The rule reads the balances the trip already computes. It fires for nobody
while the trip is in the future, and for nobody once everything is settled,
which is the state the whole feature is trying to reach.

**One reminder, the morning after the trip ends.** Category `expenses`, so it
obeys the preference people already have. One message per person who is not
square, naming the single largest transfer they are part of and linking to the
balances screen. Dedupe key `settlement_reminder:<trip>:<user>` — one per trip,
not one per day. Nobody wants a daily debt collector in their Telegram.

Empty state: a trip where everything is settled produces no item and no
message, and the balances screen already says "Everything is settled." Failure:
a notification that cannot be delivered is the outbox's problem, as always.

## What it deliberately does not do

- **It does not chase.** One reminder, once, ever. If the group ignores it, the
  product has said its piece.
- No escalation, no "Anna is still waiting" messages to other people, no
  nudging on somebody else's behalf. Four to eight friends do not need a
  collections department.
- No new settlement UI. Recording a payment already works from both the app and
  the bot.
- No reminder to the creditor to confirm receipt. Either party can mark a
  transfer settled today.

## Acceptance criteria

- [ ] A member whose balance is zero gets no attention item and no message,
      before or after the trip.
- [ ] Before the end date nobody gets the item, whatever their balance.
- [ ] The day after the end date, a member owing €40 sees one `warning` item
      naming the amount and the person.
- [ ] A member who is owed money sees an `info` item, not a `warning`.
- [ ] Running the scheduler's tick twenty times enqueues exactly one
      notification per unsettled member.
- [ ] Once the outstanding transfers are recorded as settled, both the item and
      any future reminder stop.
- [ ] An archived trip produces neither.
- [ ] A member who has turned off the `expenses` category gets no message, but
      still sees the attention item — the item is not an interruption.

## One thing the implementer will hit

The scheduler walks `trips.UpcomingTrips`, which filters `end_date >= today`.
Every existing reminder is about a trip that has not finished; this one is
about a trip that just did, so it needs its own query — recently-ended,
non-archived trips — rather than a new branch inside the existing loop.

## Existing rules it touches

**Notifications go through the outbox and need a dedupe key** — the key above
is what stops the scheduler sending this every minute, and it is the part most
likely to be got wrong. **Money is integers** — the rule reports the balance as
computed, it does not re-derive anything. **Time** — "the trip is over" is a
calendar date in the trip's timezone, not an instant, so a trip ending Sunday
nudges on Monday morning local, not at midnight UTC.

## Resolved while building

**The hour.** 09:00 in the trip's timezone. There was no existing local-hour
convention to match — the pre-trip reminders fire on whichever tick the day
turns on, which is 00:00 UTC — so this rule sets one rather than inheriting a
midnight ping for a message about somebody's money.

**The category.** Not `expenses`, as this document assumed. That category is
the firehose — a ping for every bill anybody records — and it is the one
category that defaults to *off*, so the nudge would have reached nobody. It
goes out under `reminders`, which already means "a one-off, time-triggered
message", and the Settings hint was widened to say so.
