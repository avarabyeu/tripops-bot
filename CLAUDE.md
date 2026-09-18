# TripOps — working notes for Claude Code

A group trip coordinator that lives inside Telegram: a bot for quick actions
and notifications, a Mini App for editing, one Go backend behind both.

The product promise is that anyone can open the chat, tap the trip, and
understand its whole state in ten seconds. Every technical decision below
serves that.

## Production is the operator's, not yours

**Never deploy, and never touch a production host, unless this session
explicitly asks you to.** Not when the configuration is finished, not when a
blocker is reported resolved, not when it is obviously the next step. Being
told that something is ready is information, not an instruction to act on it.

That covers everything that reaches a deployment or a live host:

- `task deploy:*`, and any `docker compose` command carrying
  `docker-compose.deploy.yml`, `--env-file .env.production` or a remote
  `--context`;
- `docker` against a remote context at all, including read-only `ps`,
  `inspect` and `logs`;
- registering or deleting a Telegram webhook, and anything else that changes
  state a real user can see;
- migrations against a production database.

What you *should* do instead: prepare the change, validate it locally, say
exactly which command the operator needs to run and what it will do. Then
stop. If you genuinely need a fact from the live host to finish the work, ask
for permission or ask them to run the command and paste the output.

"Explicitly asks" means a request to perform the action — "deploy it", "run
task deploy:up", "go ahead and push". It is not implied by a green light on a
prerequisite.

## Committing is the operator's call too

**Never `git commit` or `git push` unless this session explicitly asks.** Not to
"tidy up" at the end of a change, not because the work is finished and the tree
is dirty, not because the previous change was committed. Leave the working tree
as it is and say what is uncommitted; the decision about what enters history,
and when, is the author's.

"Explicitly asks" means a request to do it — "commit this", "push it", "go
ahead and push". It is not implied by finishing the work.

The same goes for anything else that leaves the working tree: creating
branches, tags, rebasing, amending, or touching a remote.

## Pushing ends the turn

After `git push`, say that CI was triggered and stop. **Do not poll `gh run`
for the result** unless the session asks for it. Watching a run to completion
holds the turn open for minutes and reports what the author can already see.

## How work flows

```
product-owner  →  architect  →  backend-developer   →  test-engineer
docs/features/    docs/specs/     frontend-developer     test/, *_test.go
```

- **product-owner** decides what is worth building and writes a request with
  testable acceptance criteria.
- **architect** makes the technical decisions — schema, module boundaries, API
  surface — and cuts them into ordered tasks, each of which compiles and could
  ship alone. It sends a request back when it hides a product decision.
- **backend-developer** and **frontend-developer** implement the tasks in
  order — Go and `miniapp/` respectively — and say so rather than diverging
  when the spec turns out to be wrong.
- **test-engineer** tests the *feature request*, not the specification. The
  criteria are the contract.

Not every change needs the whole chain. A bug fix or a one-file change goes
straight to a developer. Ask for a spec when a change adds a table, crosses
more than one module, or would let the bot and the Mini App disagree.

## Commands

Use `task` (Taskfile.yml); it is what CI runs.

```bash
task                  # list everything
task run              # backend against local SQLite (no Docker needed)
task test             # full suite: unit + integration, on SQLite
task check            # format check, lint, vet, tests, build — run before finishing
task fmt              # gofumpt + import grouping, via golangci-lint
task app:dev          # Mini App dev server on :5173
task migrate:status   # schema version and pending migrations
task migrate:new -- add_trip_notes    # scaffold a migration pair
task docker:up        # PostgreSQL + backend + Mini App
```

Run `task test` against PostgreSQL too before changing anything in a
repository: `task test:integration`.

## Architecture in one paragraph

A modular monolith. `internal/<module>/` holds one domain each — model, repo,
service — and modules talk through Go services, never HTTP. `internal/api`
(chi) and `internal/telegram` are adapters over the same services, which is the
only thing keeping the bot and the Mini App from disagreeing about the rules.
`cmd/tripops` is a urfave/cli app; `serve` runs the API, the bot, the reminder
scheduler and the notification worker in one process.

Read `docs/architecture.md` before making a structural change, and record any
significant decision in `docs/product-decisions.md`.

## Rules that are easy to break by accident

**Authorization goes through `trips.Access`.** Every trip-scoped API route is
mounted under the `tripAccess` middleware, which loads the trip and the
caller's membership. Handlers call `access.RequireManage()` and friends. Never
add a trip-scoped route outside that subrouter, and never check a role by hand
in a handler.

A non-member gets `not_found`, not `forbidden`. Confirming that a trip exists
to someone who was not invited leaks more than it helps.

**The database has to work on PostgreSQL and SQLite.** That is what lets the
whole test suite run with no infrastructure. Repositories use GORM and stay
inside the portable subset:

- no `FILTER`, no array parameters, no `unnest`, no `jsonb` operators, no
  advisory locks, no `SELECT … FOR UPDATE SKIP LOCKED`;
- aggregate with `count(CASE WHEN … THEN 1 END)`, pass id lists as
  `IN ?` with `core.IDStrings`, upsert with `clause.OnConflict`;
- timestamps come from Go in UTC, never from `now()`.

Migrations are plain SQL in `migrations/sql/`, run by golang-migrate, in one
directory for both engines. A shipped migration is never edited — add a new
one. The header of `000001_initial_schema.up.sql` lists the allowed types.

**Money is integers.** `core.Money` is minor units. Splits go through
`expenses.ComputeShares`, which guarantees the shares sum exactly to the total;
balances depend on that. No floats, ever. `core.DistributeEqually` and
`DistributeByWeight` handle the leftover cents.

**Time.** Instants are `time.Time` in UTC; the trip's timezone is a separate
column and conversion happens at the edges (`trip.Location()`). Calendar dates
are `core.Date`, not timestamps: "23 September" means the same thing to
everyone on the trip.

Any instant accepted from a client goes through `core.UTC` / `core.UTCPtr`
before it is stored. Skipping that does not fail loudly — it shifts the value
by the caller's offset and makes range queries quietly miss, because both
engines compare the instant as written.

**Notifications go through the outbox.** Domain code calls
`notify.Enqueue`; the Telegram worker delivers. Give every scheduled reminder a
`notify.DedupeKey(...)` — that is what lets the scheduler re-evaluate its rules
every minute without pinging anyone twice. Domain packages must not import
`internal/telegram`.

**The activity log must never fail an operation.** `activity.Log` swallows its
error into the process log on purpose.

## Testing

Domain logic is tested without a database: permissions, splitting, settlement,
capacity, progress, voting. `test/` holds the integration and end-to-end
suites, which build a real application over a temporary database via
`internal/testsupport`.

Write the test alongside the logic. Anything arithmetic (splitting, balances,
settlement) gets property-style coverage — the invariants are "shares sum to
the total" and "transfers clear every balance".

## Scope

Out of scope for the MVP, deliberately: payment providers, booking APIs, live
location, route planning, weather, AI planning, a social feed, anything
multi-tenant. TripOps records that money moved; it never moves money. Do not
add an abstraction for these — add it when there is a second implementation.

## Style

Boring, explicit Go. Hand-written SQL-ish GORM queries over clever generics.
Comments explain *why*, never restate the code. Match the surrounding file.

Formatting is `golangci-lint fmt` (gofumpt plus import grouping), not bare
`gofmt` — one tool and one config for both formatting and linting. Run
`task fmt` rather than reaching for `gofmt -w`, and run it *before* committing:
the pre-commit hook in `.githooks` rejects unformatted code, and CI only
validates (`fmt --diff`), it never reformats.
