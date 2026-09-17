# Data model

One schema, two engines. PostgreSQL in production; SQLite for development, CI
and every test. That constraint shapes most of what follows.

## Conventions

| Concern | Choice | Why |
| --- | --- | --- |
| Primary keys | `VARCHAR(36)` UUIDv4, generated in Go | PostgreSQL has a `uuid` type and SQLite does not; one portable representation beats two |
| Instants | `TIMESTAMP`, always UTC, written by the application | `now()` differs between engines and would split a write across two clocks; client-supplied times go through `core.UTC` first, since both engines compare an instant as written and an unconverted offset shifts it |
| Calendar dates | `VARCHAR(10)`, `YYYY-MM-DD` | "23 September" is not an instant; a trip's dates mean the same thing to everyone on it |
| Money | `BIGINT` minor units + a 3-letter currency | integers only; see below |
| JSON blobs | `TEXT` | nothing queries inside them, and `jsonb` is PostgreSQL-only |
| Enumerations | `VARCHAR` + `CHECK` | extending a check constraint is easier than an enum type, and it works on both |
| Booleans | `BOOLEAN` | both engines accept it |

Timezone lives on the trip, never on a timestamp. Everything is stored in UTC
and converted at the edges with `trip.Location()`.

## Tables

```
users
  └── trips ── trip_members ── trip_invites
                    │
     ┌──────────────┼───────────────┬──────────────┬─────────────┐
     ▼              ▼               ▼              ▼             ▼
  events        decisions        vehicles    accommodations  checklists
     │              │               │              │             │
event_          decision_      vehicle_     accommodation_  checklist_
participants    options        passengers   guests          items
                    │
              decision_votes

  expenses ── expense_participants        settlements
  attachments        notifications / notification_preferences
  activity_log
```

### Identity and access

- **users** — one row per Telegram account. `telegram_id` is the natural key;
  `chat_id` is where the bot can reach them and is null until they have opened
  a chat with it, which is why a notification can be *skipped* rather than
  failed.
- **trip_members** — membership, with `role` (owner/admin/member) and `status`
  (invited/active/declined/removed). A partial unique index
  (`WHERE role = 'owner'`) makes an ownerless or two-owner trip impossible.
  `display_name` is per trip: groups know each other by nicknames.
- **trip_invites** — a random `token` is what travels in a deep link, never the
  trip id. `max_uses = 0` means unlimited, the right default for a link dropped
  into a group chat. Redemption increments `uses` in a conditional `UPDATE`, so
  two people racing on a single-use link resolve safely.

Removing somebody is a status change, not a delete: their votes and expenses
stay part of the trip's history.

### Money

Every amount is an integer number of cents. Floating point never touches one.

`expense_participants` holds one row per participant of an expense with the
resolved `share_minor`, plus the `weight` it was derived from (cents for a
custom split, basis points for a percentage, null for an equal one). **The rows
of an expense always sum exactly to `expenses.amount_minor`**, whatever the
split type — that invariant is asserted by property tests and is what lets the
balance query be a plain `SUM`.

A balance is `paid − owed + net settled`. `settlements` records transfers
between two members; only rows with `status = 'settled'` move a balance, and
`pending` rows are plans. TripOps records that money moved; it never moves it.

### The outbox

`notifications` is a queue, not a log. `dedupe_key` is uniquely indexed, which
is what makes every scheduled reminder idempotent and lets the scheduler
re-evaluate its rules every minute without pinging anybody twice. Delivery
claims a row by flipping `status` to `sending` and stamping `claimed_at`; a row
stuck there for five minutes (a worker died) becomes claimable again. That
replaces `SELECT … FOR UPDATE SKIP LOCKED`, which SQLite does not have.

## The portable subset

Migrations and repositories stay inside this subset. Everything here is a rule
a change can break silently, because the tests run on SQLite and production
does not.

**Never use:** `FILTER`, array parameters or `unnest`, `jsonb` operators,
advisory locks, `SELECT … FOR UPDATE SKIP LOCKED`, `RETURNING` on multi-row
writes, `SERIAL`, enum types, `now()`, `INTERVAL` arithmetic.

**Instead:**

| Need | Portable form |
| --- | --- |
| Conditional aggregate | `count(CASE WHEN … THEN 1 END)` |
| List parameter | `IN ?` with `core.IDStrings(ids)` |
| Upsert | `clause.OnConflict{...}` |
| "Now" | a Go `time.Time` in UTC |
| Time arithmetic | compute in Go, pass the instant |
| Row locking | a conditional `UPDATE` plus `RowsAffected` |

Correlated subqueries, partial indexes, `CHECK` constraints and inline
`REFERENCES` all behave the same on both and are used freely.

## Migrations

Plain SQL in `migrations/sql/`, embedded in the binary and run by
golang-migrate: one directory for both engines, so there is exactly one
description of the schema.

```bash
task migrate:status
task migrate:up
task migrate:new -- add_trip_notes
```

A migration that has shipped is never edited — add a new one. Each `.up.sql`
has a `.down.sql` that actually reverses it. On PostgreSQL the driver runs with
`MultiStatementEnabled`, because pgx speaks the extended protocol and would
otherwise refuse a file with more than one statement.

GORM's struct tags describe the same tables for querying, but they do **not**
create them; `AutoMigrate` is not used anywhere. The integration suite is what
keeps the tags and the SQL honest: it exercises every repository against a real
database, so a mismatch fails a test rather than a deployment.
