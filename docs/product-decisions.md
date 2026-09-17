# Product decisions

Decisions worth remembering, with the reasoning. Newest last.

## Go instead of the specified TypeScript stack

The specification recommends Node, Fastify/NestJS and Prisma/Drizzle. The
project was commissioned in Go, so the stack maps across: chi for routing,
GORM for data access, hand-written validation in place of Zod, and a small
Telegram client in place of grammY. Nothing in the product depends on the
runtime; the module boundaries the spec asks for are the same either way.

## Trips, members and invites are one package

The spec lists `trips/` and `members/` as separate modules. In Go they would
form an import cycle: creating a trip needs a member row, and checking
membership needs the trip. They are one aggregate — the thing that answers "may
this user do this" — and every other module depends on it through
`trips.Access`. Splitting them would mean an interface and a setter purely to
satisfy the compiler.

## Authorization is a middleware, not a handler convention

Every trip-scoped route is mounted under one middleware that loads the trip and
the caller's membership. A handler that forgot the check would have nothing to
read, so the check cannot be skipped by accident. Handlers then express intent
(`access.RequireManage()`) rather than mechanism.

A non-member gets `not_found` rather than `forbidden`. Trip ids are not secret,
but confirming that one exists to somebody who was not invited leaks more than
it helps.

## Nested routes instead of the sketched flat ones

`PATCH /trips/{tripID}/events/{eventID}` rather than `PATCH /events/{id}`. The
nesting is what lets one middleware authorize everything, and it turns an id
borrowed from another trip into a 404 instead of a leak.

## Money is integers, and splits are exact

`core.Money` is minor units. Floating point never touches an amount. Splitting
distributes leftover cents with a largest-remainder rule so the shares of an
expense sum *exactly* to its total — asserted by property tests over thousands
of random inputs. Balances are a plain `SUM` over those shares, which is only
trustworthy because of that invariant.

Percentages are basis points (10000 = 100%) so they are integers too.

## The settlement algorithm

Minimising transfers exactly is NP-hard (it is partition in disguise), so the
implementation does the two things that matter in practice:

1. **Pay off exact matches first.** If Peter owes 13 and Kolya is owed 13, that
   is one transfer with no residue — which greedy matching by size can miss.
2. **Then greedily settle the largest debtor against the largest creditor.**
   Each step zeroes at least one person, so with *n* non-zero balances it never
   emits more than *n − 1* transfers.

Output is deterministic (ties broken by member id), because the UI presents the
suggestions as if they were stable facts.

Balances that do not sum to zero are reported as an internal error rather than
absorbed into somebody's debt: it would mean a bug upstream.

## Recording money, not moving it

No payment provider, by explicit scope. "Mark as settled" writes a row saying a
transfer happened. Only `settled` rows move a balance; `pending` ones are plans.
The UI says so in as many words, because a button next to an amount invites the
assumption that it pays.

## Voting advises, organisers decide

A decision closes itself at its deadline, but nothing is ever applied
automatically. An organiser resolves it explicitly, and may resolve against the
count — "the group voted for Hotel A" and "we are staying at Hotel A" are
different statements, and only the second one is a plan. Votes are not
anonymous: in a group of friends, knowing who wants what is the point.

## The attention engine is rules, not scoring

Deterministic predicates, each producing a sentence a person would say out loud
("Sasha has not confirmed accommodation"). No model, no weights. Items the
caller must act on personally sort first. Every rule fails independently: a
broken query leaves its card empty instead of taking down the screen somebody
opened to find out what is going on.

## Notifications go through an outbox

Domain code writes a row; a Telegram worker drains it. That keeps domain logic
free of network calls, makes delivery retryable, and lets the scheduler be
stupid — every reminder carries a dedupe key, so re-evaluating the same rule a
minute later inserts nothing. Preferences are applied at enqueue time, so a
muted category never reaches the table.

Preferences are per user rather than per trip: somebody who does not want
expense pings does not want them on any trip, and a per-trip matrix is more
settings UI than this product can justify.

## GORM, with two engines

PostgreSQL in production; SQLite for development, CI and the entire test suite,
which is why `go test ./...` needs no Docker. The cost is a portable-SQL
discipline — no `FILTER`, no array parameters, no advisory locks, no
`SKIP LOCKED` — documented in `docs/data-model.md` and enforced by the fact
that the tests run on the constrained engine.

The outbox claims rows by flipping a status and stamping `claimed_at`, with a
five-minute staleness window, instead of `SELECT … FOR UPDATE SKIP LOCKED`.

## golang-migrate over ORM auto-migration

`AutoMigrate` cannot express a partial index, drop a column, or be reviewed in
a pull request. Migrations are plain SQL files run by golang-migrate, embedded
in the binary. They live in **one** directory rather than one per dialect: the
subset of SQL both engines accept is wide enough for this schema, and two
copies would be one to forget.

GORM's struct tags describe the tables for querying only. The integration suite
is what keeps the tags and the SQL honest.

On PostgreSQL the migration runs on a connection of its own, in pgx's simple
query mode, so the server parses the whole file. The obvious alternative —
golang-migrate's `MultiStatementEnabled` — splits on every `;` byte without
understanding comments or string literals, which cut a `CREATE TABLE` in half
at the first semicolon inside a comment. The application pool keeps the
extended protocol and its prepared statements; the migration connection is
closed with the migration. SQLite reuses the application connection instead,
because an in-memory database exists only inside the connection that created
it.

## A hand-written Telegram client

The bot needs six API methods. A full SDK would add a dependency and a lot of
surface for no benefit, and the client is the one place where exact control
over HTML escaping matters. Callback payloads use a 22-character base64 id
(`core.ID.Compact`) because Telegram caps `callback_data` at 64 bytes, which is
not enough for two dashed UUIDs.

## Invite tokens, never trip ids

Deep links carry a random token. Trip ids never appear in chat history.
Redemption increments a counter in a conditional `UPDATE`, so two people
racing on a single-use link resolve safely, and re-joining with an existing
membership reactivates it instead of failing — which is what tapping an old
link a second time should do.

## Timezone on the trip

Every instant is stored in UTC; the trip carries an IANA timezone and display
converts at the edges. Trip start and end are calendar dates, not instants:
"23 September" means the same thing to everyone on the trip, wherever they read
it from.

Instants arriving from a client are normalised with `core.UTC` / `core.UTCPtr`
at the service boundary, not merely by convention. Clients send times in
whatever offset their phone is in, and the storage layer compares instants as
written: SQLite compares the text, and a PostgreSQL `timestamp` column drops
the offset without converting. An unnormalised `12:09+02:00` therefore ranges
as though it were 12:09 UTC — which moved every deadline by the client's offset
and made the reminder windows miss the decisions they were meant to catch.
`TestInstantsAreStoredInUTC` guards it.

## No CSS framework in the Mini App

Telegram supplies the palette through CSS variables and changes them with the
user's theme. Hand-written CSS over those variables is smaller than a utility
framework and cannot fight the host's theming. The app ships one 7 kB
stylesheet.

## A 60-line router

Mini Apps are navigated with Telegram's own back button, so what the app needs
is a screen stack, not URL routing. The stack drives `BackButton` directly.
