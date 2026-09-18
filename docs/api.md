# API

REST over JSON, consumed by the Telegram Mini App. Base path `/api/v1`.

## Authentication

Every request carries the Telegram Mini App launch parameters:

```
Authorization: tma <initData>
```

The server re-verifies the HMAC on **every** request against the bot token,
checks `auth_date` against `TELEGRAM_INITDATA_TTL`, and maps the Telegram user
onto an internal one, creating it on first contact. There is no session, no
cookie and no token exchange. `X-Telegram-Init-Data` is accepted as an
alternative header.

In development only, setting `DEV_USER_TELEGRAM_ID` lets unsigned requests
through as that user so the Mini App can be opened in a desktop browser. The
server refuses to honour it unless `APP_ENV=development`.

## Authorization

Three checks, in order, for every trip-scoped route: authenticated caller,
member of *this* trip, sufficient role. A non-member gets `404`, not `403`.

| Role | May |
| --- | --- |
| owner | everything, plus archive, delete, transfer ownership, and edit any expense |
| admin | edit the trip, manage people, create and edit all trip content except expenses |
| member | view, update their own participation, vote, add expenses, tick checklist items |

Expenses are the one exception to "admin can edit all trip content": only the
person who recorded one and the owner may change it.

## Errors

One shape, always:

```json
{ "error": { "code": "invalid", "message": "title is required", "fields": { "title": "is required" } } }
```

| Code | Status | Means |
| --- | --- | --- |
| `invalid` | 400 | failed validation; `fields` names what |
| `unauthorized` | 401 | missing or bad init data |
| `forbidden` | 403 | authenticated, but the role is insufficient |
| `not_found` | 404 | absent, or not visible to this caller |
| `conflict` | 409 | the state does not allow it (car full, voting closed, trip archived) |
| `internal` | 500 | a bug; details are logged, never returned |

Validation collects every problem in one response rather than failing on the
first, so a form can show all of its errors at once.

## Conventions

- Instants are RFC 3339 (`2026-09-23T17:00:00Z`); calendar dates are
  `YYYY-MM-DD`.
- **Money is always integer minor units** in a field ending `_minor`. The
  client converts for display. Percentages are basis points (10000 = 100%).
- `PATCH` bodies are sparse: an omitted field is unchanged. To clear an
  optional value, use the matching `clear_*` flag (`clear_end_at`,
  `clear_assignee`).
- Unknown JSON fields are rejected, so a client typo surfaces immediately.
- Collections come back wrapped (`{"events": [...]}`), never as a bare array.

## Endpoints

### Identity

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/me` | the caller, plus `start_param` from the deep link |
| `GET` | `/me/notification-preferences` | |
| `PATCH` | `/me/notification-preferences` | per user, not per trip |

### Trips

| Method | Path | Role |
| --- | --- | --- |
| `GET` | `/trips` | — |
| `POST` | `/trips` | — (creator becomes owner) |
| `GET` | `/trips/{tripID}` | member |
| `PATCH` | `/trips/{tripID}` | admin; archiving is owner-only |
| `DELETE` | `/trips/{tripID}` | owner — irreversible, takes everything with it |
| `GET` | `/trips/{tripID}/dashboard` | member — the whole home screen in one call |
| `GET` | `/trips/{tripID}/activity` | member |

The currency can only be changed while the trip has no expenses. Amounts are
minor units of it with no rate history, so changing it later would silently
reinterpret every recorded expense; the attempt returns `conflict`.

Deleting a trip removes its people, timeline, decisions, logistics,
accommodation, checklists, expenses and settlements. There is no soft delete:
archiving covers "we are done with this trip", so a hidden copy of one that
should never have existed would serve nobody.

### People and invites

| Method | Path | Role |
| --- | --- | --- |
| `GET` | `/trips/{tripID}/members` | member |
| `PATCH` | `/trips/{tripID}/members/{memberID}` | self, or admin for anyone; roles owner-only |
| `DELETE` | `/trips/{tripID}/members/{memberID}` | self (leave), or admin |
| `POST` | `/trips/{tripID}/transfer-ownership` | owner |
| `GET`/`POST` | `/trips/{tripID}/invites` | admin |
| `DELETE` | `/trips/{tripID}/invites/{inviteID}` | admin |
| `GET` | `/invites/{token}` | any authenticated user — preview before joining |
| `POST` | `/invites/{token}/join` | any authenticated user; idempotent |

### Timeline

| Method | Path | Role |
| --- | --- | --- |
| `GET` | `/trips/{tripID}/events` | member |
| `POST` | `/trips/{tripID}/events` | admin |
| `GET`/`PATCH`/`DELETE` | `/trips/{tripID}/events/{eventID}` | member / admin / admin |
| `POST` | `/trips/{tripID}/events/{eventID}/rsvp` | self; `member_id` needs admin |

A new event includes every active member as `undecided` unless
`participant_ids` says otherwise.

### Decisions

| Method | Path | Role |
| --- | --- | --- |
| `GET`/`POST` | `/trips/{tripID}/decisions` | member / admin |
| `GET` | `/trips/{tripID}/decisions/{decisionID}` | member |
| `POST` | `…/vote` | member — one vote each, changeable while open |
| `POST` | `…/close` | admin — also happens automatically at the deadline |
| `POST` | `…/resolve` | admin — states the outcome; never automatic |
| `POST` | `…/cancel` | admin |

### Logistics

| Method | Path | Role |
| --- | --- | --- |
| `GET`/`POST` | `/trips/{tripID}/vehicles` | member / admin |
| `PATCH`/`DELETE` | `/trips/{tripID}/vehicles/{vehicleID}` | admin |
| `POST` | `…/join`, `…/leave` | member — taking or giving up a seat |

The driver occupies a seat. Overfilling a vehicle, or seating somebody who is
already in another one, is a `conflict`.

### Accommodation

| Method | Path | Role |
| --- | --- | --- |
| `GET`/`POST` | `/trips/{tripID}/accommodations` | member / admin |
| `PATCH`/`DELETE` | `/trips/{tripID}/accommodations/{placeID}` | admin |
| `POST` | `…/guest-status` | self; `member_id` needs admin |

### Checklists

| Method | Path | Role |
| --- | --- | --- |
| `GET` | `/trips/{tripID}/checklists` | member — shared lists plus their own personal ones |
| `POST` | `/trips/{tripID}/checklists` | admin for shared, anyone for personal |
| `PATCH`/`DELETE` | `/trips/{tripID}/checklists/{listID}` | owner of the list, or admin |
| `POST` | `/trips/{tripID}/checklists/{listID}/items` | member |
| `PATCH`/`DELETE` | `/trips/{tripID}/checklist-items/{itemID}` | see below |

Anyone may tick a shared item that is unassigned or assigned to them; editing
the text or the assignee of a shared item is an organiser action.

### Expenses and balances

| Method | Path | Role |
| --- | --- | --- |
| `GET`/`POST` | `/trips/{tripID}/expenses` | member — everybody pays for something |
| `PATCH`/`DELETE` | `/trips/{tripID}/expenses/{expenseID}` | whoever recorded it, or the owner |
| `GET` | `/trips/{tripID}/expenses/report` | member — the whole ledger in one call |
| `GET` | `/trips/{tripID}/balances` | member — balances, suggested transfers, settlements |
| `POST` | `/trips/{tripID}/settlements` | either party, or admin |
| `POST` | `/trips/{tripID}/settlements/{settlementID}/settle` | either party, or admin |
| `DELETE` | `/trips/{tripID}/settlements/{settlementID}` | either party, or admin |

Creating an expense:

```json
{
  "title": "Fuel",
  "amount_minor": 12000,
  "category": "fuel",
  "paid_by": "<member id>",
  "split_type": "equal",
  "participants": [{ "member_id": "…" }, { "member_id": "…" }]
}
```

`participants` defaults to every active member with an equal split. For
`custom_amount` the weights are minor units; for `percentage` they are basis
points. Either way they must add up exactly, and the response echoes the
resolved per-member shares.

Editing an expense replaces the whole split when `participants` is present,
which is the point of it: a bill divided between the people who were on the
trip at the time goes stale the moment somebody else joins.

Note the role: whoever recorded the expense, or the trip owner. Organisers can
do almost everything else on a trip but not this — see
`docs/product-decisions.md`.

`GET /balances` returns the suggested transfers already minimised — see
`docs/product-decisions.md` for the algorithm.

`GET /expenses/report` answers the other question — not "what should I do
next" but "what happened":

```json
{
  "currency": "EUR",
  "total_minor": 36000,
  "count": 3,
  "per_person_minor": 12000,
  "by_category": [{ "category": "accommodation", "total_minor": 21000, "count": 1, "percent": 58 }],
  "members": [
    {
      "member_id": "…",
      "display_name": "Ann",
      "paid_minor": 15000,
      "paid_count": 2,
      "share_minor": 13000,
      "share_count": 3,
      "settled_minor": 0,
      "balance_minor": 2000
    }
  ],
  "expenses": [ "…the same objects as GET /expenses, shares included…" ]
}
```

`balance_minor` is the same figure `GET /balances` reports, restated next to
the paid and share numbers it came from: the report reuses that query rather
than deriving the amounts a second way. `per_person_minor` is the average cost
of the trip, which is not what anybody owes unless every bill was split evenly.

### Attachments

| Method | Path | Role |
| --- | --- | --- |
| `GET` | `/trips/{tripID}/attachments?owner_type=&owner_id=` | member |
| `POST` | `/trips/{tripID}/attachments` | member |
| `DELETE` | `/trips/{tripID}/attachments/{attachmentID}` | uploader, or admin |

Either a Telegram `file_id` or an external `url`, never both. TripOps stores the
handle, not the bytes.

### Operations

`GET /healthz` — the process is up. `GET /readyz` — the database answers.
Neither requires authentication.

## Deviation from the original specification

The spec sketched flat routes (`PATCH /events/:id`). They are nested under
`/trips/{tripID}` instead, so a single middleware can authorize every one of
them and an id borrowed from another trip is a `404` rather than a leak.
