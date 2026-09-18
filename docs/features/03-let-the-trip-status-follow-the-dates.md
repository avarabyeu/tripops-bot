# Let the trip status follow the dates

## The question this answers

None, directly. It stops the product contradicting itself.

## Who and when

Everybody, every time they open the trip list after a trip has happened.

## Why now

`core.TripStatus` has four values — `planning`, `active`, `completed`,
`archived`. Nothing in the codebase ever writes `active` or `completed`. Every
trip is created `planning` and stays there until somebody archives it, so a
brevet ridden three months ago still says "planning" in the trip list, in the
bot's trip menu and on the Settings screen.

TripOps describes itself as a shared trip state machine. The state never
changes. It is a small thing that makes the product look unfinished in the one
place every user sees first, and the fix is a handful of lines in a scheduler
that already runs every minute and already iterates trips.

## How it works

The scheduler's tick sets, in the trip's own timezone:

- `planning` → `active` on the morning of the start date;
- `active` → `completed` the day after the end date.

`archived` is never touched by the scheduler, in either direction: it is the
one status a person chose, and a manual choice outranks a derived one. A trip
whose dates are edited backwards settles to whatever its dates now say.

No notification, no activity entry. Nobody needs to be told that Saturday
arrived.

## What it deliberately does not do

- No new status values. "Cancelled" is a different conversation.
- No behaviour changes attached to the status. `Writable()` keeps meaning "not
  archived"; a completed trip stays fully editable, because people add the last
  expenses after they get home.
- No auto-archiving. Deciding a trip is done with is a person's call.

## Acceptance criteria

- [ ] A trip starting today is `active` after one tick; one starting tomorrow
      is still `planning`.
- [ ] A trip that ended yesterday is `completed`; one ending today is still
      `active`.
- [ ] An archived trip stays archived through any number of ticks, whatever its
      dates.
- [ ] A completed trip still accepts a new expense — `Writable()` is unchanged.
- [ ] Ticking repeatedly writes the status once, not on every pass.
- [ ] A trip in `Pacific/Auckland` flips on Auckland's calendar, not UTC's.

## Existing rules it touches

**Time** — the transition is a calendar comparison in the trip's timezone;
doing it in UTC gets it wrong by a day for half the world. **Two engines** — a
plain `UPDATE ... WHERE status = ?` is portable; nothing clever is needed.

## Open questions

None.
