# Edit a trip after creating it

## The question this answers

"I typed the wrong date when I made this — can you fix it?"

## Who and when

Anyone who created a trip, within about a minute of creating it. A trip made
through the bot takes two answers — title and dates — and inherits the server's
default timezone and currency. Everything else is unchangeable forever.

## Why now

`PATCH /trips/{tripID}` exists, is tested, and is reachable from nothing. The
Mini App client has `api.trips.update` and no screen calls it. The bot's
settings view tells the user, in as many words, *"Editing the trip, people and
content happens in the Mini App"* — and the Mini App's Settings screen shows
those five fields as read-only text.

So this is not a new capability. It is a promise the product already makes and
cannot keep, and the fix is one form over an endpoint that is already there.

The currency case is the sharp one: a trip created through the bot gets the
server default. If that is wrong, every expense on the trip is recorded in the
wrong currency and there is no way back.

## How it works

The Settings screen's trip card gains an **Edit** action, admin and owner only,
opening a sheet over the fields that already exist on `UpdateInput`: title,
description, dates, timezone, currency.

- **Currency** is editable only while the trip has no expenses. Once money is
  recorded the field is shown disabled with the reason, because each expense
  stores its own currency and changing the trip's would leave a ledger that
  sums across two of them.
- **Dates** may move freely. Events keep their instants and are not dragged
  along.
- **Timezone** changes what everyone sees, not what is stored; instants are
  already UTC.

Empty state: none, the trip always exists. Failure: the sheet keeps the values
and shows the field errors the validator returns, as every other sheet does.

## What it deliberately does not do

- No archive control. That is a separate decision with a separate blast radius.
- No moving events when the dates change, and no warning about events that now
  fall outside the range. If that turns out to matter it is an attention rule,
  not a modal.
- No edit history. The activity log already records that the trip was updated.

## Acceptance criteria

- [ ] An admin changes the title, and the trip list, the bot's trip menu and
      the dashboard all show the new one.
- [ ] A plain member gets `403` from `PATCH /trips/{id}` and sees no Edit
      action.
- [ ] Changing the currency on a trip with no expenses succeeds; on a trip with
      one recorded expense it is rejected, and the app does not offer the field.
- [ ] Moving the start date later leaves every event's `start_at` untouched.
- [ ] Changing the timezone changes the times rendered on the timeline without
      changing any stored instant.
- [ ] An archived trip rejects the edit with `conflict`, like every other write.

## Existing rules it touches

`trips.Access` — the route is already inside the subrouter and
`RequireManage()` already guards the handler, so nothing new. Money: the
currency rule above is the whole reason this needs a decision rather than a
form. Time: timezone is a display concern, dates are `core.Date`, neither is an
instant.

## Open questions

None.
