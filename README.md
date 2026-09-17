# TripOps

A group trip coordinator that lives inside Telegram.

Keep the whole trip in one place — people, timeline, decisions, logistics,
checklists and money — so nobody has to scroll back through the group chat to
find out when you are leaving or who owes whom.

```
BREVET ŁÓDŹ 200
23–24 September

👥 People          5 / 6 confirmed
🚗 Transport       2 vehicles
🏠 Accommodation   6 / 6 confirmed
📅 Next            Departure · 23 Sep 19:00
💰 Expenses        €184 total
🗳 Decisions       1 pending
🎒 Checklist       18 / 23 complete

⚠️ Sasha has not confirmed accommodation
```

Two front doors over one backend: a **Telegram bot** for quick actions and
notifications, and a **Telegram Mini App** for anything that needs a screen.

---

## Quick start

Requires Go 1.27+, Node 20+, and [Task](https://taskfile.dev). No database
server: development defaults to a local SQLite file.

```bash
git clone https://github.com/avarabyeu/tripops-bot.git && cd tripops-bot
cp .env.example .env          # optional; only needed for the bot

task run                      # backend on :8080, migrations applied
task app:dev                  # Mini App on :5173, in another terminal
task test                     # the whole suite
```

Open <http://localhost:5173>. Outside Telegram the app has no signed
credentials, so set `DEV_USER_TELEGRAM_ID=1` in `.env` to browse as a test
user. That shortcut is refused unless `APP_ENV=development`.

`task` on its own lists everything.

---

## Architecture

```
Telegram ──▶ bot adapter ┐
                         ├──▶ domain services ──▶ GORM ──▶ PostgreSQL | SQLite
Mini App ──▶ REST API ───┘
```

A modular monolith. `internal/<module>/` holds one domain each — model,
repository, service — and modules talk through Go services, never HTTP. The bot
and the API are adapters over the same services, which is what stops them
disagreeing about the rules.

| Path | What |
| --- | --- |
| `cmd/tripops` | the binary; a urfave/cli app |
| `internal/cli` | `serve`, `migrate`, `bot` commands |
| `internal/core` | ids, money, dates, errors, validation |
| `internal/db` | connection, dialect handling |
| `internal/trips` | trips, members, invites — the authorization root |
| `internal/{events,decisions,logistics,accommodation,checklists,expenses}` | the domains |
| `internal/{activity,notify,attention,dashboard,attachments}` | supporting domains |
| `internal/{api,telegram}` | the two adapters |
| `internal/scheduler` | reminder rules on a ticker |
| `migrations/sql` | plain SQL, embedded, run by golang-migrate |
| `miniapp` | React + TypeScript + Vite |
| `test` | integration and end-to-end suites |

More in [`docs/architecture.md`](docs/architecture.md).

**Stack:** Go 1.27 · chi · GORM · golang-migrate · urfave/cli · PostgreSQL or
SQLite · React 19 + Vite · Task.

---

## Configuration

Everything is an environment variable; [`.env.example`](.env.example) documents
each one. The essentials:

| Variable | Default | Notes |
| --- | --- | --- |
| `DATABASE_URL` | `sqlite://tripops.db` | or `postgres://user:pass@host:5432/db` |
| `TELEGRAM_BOT_TOKEN` | — | from @BotFather; without it the API runs and the bot does not |
| `TELEGRAM_BOT_USERNAME` | — | used to build invite deep links |
| `TELEGRAM_BOT_MODE` | `polling` | `webhook` in production |
| `MINIAPP_URL` | — | public HTTPS URL of the Mini App |
| `APP_ENV` | `development` | `production` disables every development shortcut |
| `DEV_USER_TELEGRAM_ID` | — | development only; browse without Telegram |

---

## Database

Production runs PostgreSQL; development, CI and the whole test suite run
SQLite, which is why the tests need no infrastructure. One schema serves both —
see [`docs/data-model.md`](docs/data-model.md) for the portable-SQL rules that
makes necessary.

```bash
task migrate:status
task migrate:up
task migrate:new -- add_trip_notes    # scaffolds the up/down pair
task db:reset                         # wipe the local SQLite file and rebuild
```

Migrations are plain SQL in `migrations/sql/`, embedded in the binary and run
by golang-migrate. `serve` applies pending ones on start-up; pass
`--skip-migrations` to leave that to a deploy step. A migration that has
shipped is never edited — add a new one.

---

## Telegram setup

1. Create a bot with [@BotFather](https://t.me/BotFather) and copy the token
   into `TELEGRAM_BOT_TOKEN`; put the bot's username into
   `TELEGRAM_BOT_USERNAME` so invite links can be built.
2. Run `task run`. In polling mode that is all: no public URL needed.
3. Publish the command menu: `task bot:info` to check the token,
   `./bin/tripops bot set-commands` to register `/start`, `/trips`,
   `/newtrip`, `/help`.
4. For the Mini App, send `/setmenubutton` to BotFather and point it at your
   `MINIAPP_URL`.
5. In production, switch to webhooks:

   ```bash
   TELEGRAM_BOT_MODE=webhook \
   TELEGRAM_WEBHOOK_URL=https://tripops.example.com \
   TELEGRAM_WEBHOOK_SECRET=$(openssl rand -hex 32) \
   task bot:webhook
   ```

   The secret is echoed back by Telegram on every update, which is how the
   endpoint tells a genuine one from anybody who guessed the path.

Invites are deep links: `https://t.me/<bot>?start=inv_<token>`. The token is
random and the trip id never appears in chat history.

---

## Mini App

```bash
task app:dev        # Vite dev server, /api proxied to the backend
task app:build      # production bundle into miniapp/dist
task app:typecheck
```

React 19, TypeScript, no CSS framework — Telegram supplies the palette through
CSS variables, so the app styles itself from those and follows the user's
theme. Mobile-first, one column, every section with a real empty state.

Serve `miniapp/dist` from any static host over HTTPS (Telegram requires it) and
set that URL as `MINIAPP_URL`.

---

## Testing

```bash
task test               # unit + integration, on SQLite
task test:unit          # fast domain tests only
task test:integration   # the same integration suite against PostgreSQL
task test:cover         # coverage report
```

Domain logic is tested without a database: permissions, expense splitting, the
settlement algorithm, vehicle and accommodation capacity, checklist progress,
voting, notification rules and Telegram init-data validation. The arithmetic is
covered by property tests — the invariants are "the shares of an expense sum to
its total" and "applying the suggested transfers clears every balance".

`test/happy_path_test.go` walks the MVP definition of done end to end: create a
trip → invite → join → event → decision → vote → expense → balances →
settlement → dashboard.

Before changing a repository, run the integration suite against PostgreSQL too.
Each test claims a schema of its own, so runs do not interfere:

```bash
task docker:pg:up
task test:integration
```

---

## Deployment

Two stacks, same images, different engine:

```bash
task docker:up        # default: backend on SQLite + Mini App — two containers
task docker:pg:up     # PostgreSQL + backend + Mini App — what production runs
task docker:logs
```

`docker-compose.yml` runs the backend on a SQLite file in a named volume, which
is enough for a group of friends and needs no database server.
`docker-compose-pg.yml` is the same stack on PostgreSQL; use it to check a
change against the production engine.

The backend builds to a static binary in a distroless image (no cgo — the
SQLite driver is pure Go). Production needs three things: this container, a
database, and the Mini App bundle on a static host. No Kubernetes, no object
storage: Telegram keeps attachment bytes and TripOps stores only the handles.

```bash
task build                                    # ./bin/tripops
./bin/tripops migrate up                      # as a deploy step, if preferred
./bin/tripops serve --skip-migrations
```

`GET /healthz` says the process is up; `GET /readyz` says the database answers.

---

## Development

```bash
task check     # format check, lint, vet, tests, build — what CI runs
task fmt       # gofumpt + import grouping, via golangci-lint
task lint      # needs `task tools` once
```

Formatting and linting come from golangci-lint with one config
(`.golangci.yml`), so there is no separate `gofmt` step to disagree with it.

[`CLAUDE.md`](CLAUDE.md) holds the working notes and the rules that are easy to
break by accident. Significant decisions are recorded in
[`docs/product-decisions.md`](docs/product-decisions.md).

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — modules, adapters, the outbox
- [`docs/data-model.md`](docs/data-model.md) — schema and the portable-SQL subset
- [`docs/api.md`](docs/api.md) — endpoints, auth, error shapes
- [`docs/product-decisions.md`](docs/product-decisions.md) — why things are the way they are

## Scope

TripOps records that money moved; it never moves it. Deliberately out of scope:
payment providers, booking APIs, live location, route planning, weather, AI
planning, social feeds, multi-tenancy.
