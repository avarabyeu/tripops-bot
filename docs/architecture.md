# Architecture

TripOps is a modular monolith in Go with two front doors — a Telegram bot and a
Telegram Mini App — over one set of domain services.

```
                    ┌──────────────┐        ┌──────────────┐
   Telegram  ──────▶│  bot adapter │        │  Mini App    │─── React/Vite
   updates          │ internal/    │        │  (browser)   │
                    │  telegram    │        └──────┬───────┘
                    └──────┬───────┘               │ REST + signed initData
                           │                       ▼
                           │              ┌──────────────────┐
                           └─────────────▶│  internal/api    │  chi router
                                          └────────┬─────────┘
                                                   ▼
                        ┌──────────────────────────────────────────┐
                        │   domain services (internal/<module>)     │
                        │  trips · events · decisions · logistics   │
                        │  accommodation · checklists · expenses    │
                        │  attachments · activity · notify          │
                        └───────────────┬──────────────────────────┘
                                        ▼
                             GORM ─▶ PostgreSQL | SQLite
```

## Why a monolith

A trip has at most a dozen people and a few hundred rows. The interesting
complexity is in the rules — who may change what, how money splits, what needs
somebody's attention — not in scale. Package boundaries give those rules a
place to live; separate services would add deployment and consistency problems
the product does not have. The modules talk through Go interfaces, so splitting
one out later is a refactor, not a rewrite.

## Modules

Each `internal/<module>` has the same three parts:

| File | Holds |
| --- | --- |
| `<module>.go` | the model, its enums, and pure logic worth testing alone |
| `repo.go` | all data access, GORM only, no rules |
| `service.go` | use cases: validate, authorize, write, then narrate and notify |

| Module | Answers |
| --- | --- |
| `trips` | who may do what — the authorization root; owns trips, members, invites |
| `events` | when things happen and who is coming |
| `decisions` | structured group questions, one vote per participant |
| `logistics` | vehicles, drivers and seats |
| `accommodation` | places to stay and who confirmed a bed |
| `checklists` | shared and personal packing lists |
| `expenses` | what was paid, how it splits, who owes whom |
| `attachments` | files and links hung off other objects |
| `activity` | the human-readable feed |
| `notify` | the notification outbox and per-user preferences |
| `attention` | the deterministic rules behind the dashboard's warnings |
| `dashboard` | composes the home screen from all of the above |

Supporting packages: `core` (ids, money, dates, errors, validation), `db`
(connection and dialect handling), `httpx` (transport plumbing), `auth`
(Telegram identity), `config`, `scheduler`, `cli`.

## The authorization root

`trips.Access` is the answer to "may this user do this to this trip". It bundles
the trip, the caller's membership and the caller. Every trip-scoped API route is
mounted under a middleware that resolves it, so a handler cannot forget:

```go
r.Route("/trips/{tripID}", func(r chi.Router) {
    r.Use(s.tripAccess)          // authenticated + member + loaded
    r.Post("/events", s.withAccess(s.handleCreateEvent))
})
```

Services then ask `access.RequireManage()`, `RequireWrite()` or
`RequireSelfOrManage(memberID)`. The bot goes through exactly the same calls.

A non-member receives `not_found`, not `forbidden`: trip ids are not secret,
but confirming one exists to someone who was not invited leaks more than it
helps.

## Telegram as an adapter

`internal/telegram` contains a small Bot API client, the screen renderers, the
update dispatcher and the notification delivery worker. It imports domain
packages; no domain package imports it. That is what keeps the bot and the Mini
App from drifting apart — both call the same service methods, so a rule can
only be implemented once.

Authentication differs by door and converges immediately:

- **Mini App** — every request carries `Authorization: tma <initData>`, whose
  HMAC is re-verified per request against the bot token. There is no session.
- **Bot** — Telegram delivers the update, so the sender is already established.

Both end at a `users.User`.

## The notification outbox

Domain code never sends a message. It writes a row:

```
service ──enqueue──▶ notifications table ──drain──▶ Telegram worker
```

That indirection makes delivery retryable, keeps domain logic free of network
calls, and lets the scheduler be stupid: every reminder carries a dedupe key, so
re-evaluating the same rule a minute later inserts nothing. Preferences are
applied at enqueue time, so a muted category never reaches the table.

## Two database engines

Production runs PostgreSQL. Development, CI and the whole test suite run
SQLite, which is why `go test ./...` needs no infrastructure at all. GORM plus a
deliberately portable schema is what makes that work; see
`docs/data-model.md` for the constraints this imposes.

## The process

`tripops serve` runs four things in one process:

1. the HTTP server (REST for the Mini App, the webhook endpoint if configured);
2. the bot — long polling in development, webhook in production;
3. the scheduler, re-evaluating reminder rules on an interval;
4. the delivery worker, draining the outbox.

They share the connection pool and a cancellable context; SIGTERM stops
accepting requests, then waits for the workers.

## Deliberate omissions

No payment providers, no booking APIs, no live location, no route planning, no
weather, no AI, no social feed, no multi-tenancy. There are no interfaces
waiting for them either: an abstraction arrives with its second implementation.
