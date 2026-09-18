# Remove the attachments module

## The question this answers

None. That is the point.

## Who and when

Nobody, ever — which is the finding.

## Why now

`internal/attachments` is 255 lines, a table, a service wired into `serve.go`
and `testsupport`, three API routes, and a section in `docs/api.md`. Nothing
reaches it. There is no Mini App screen, no bot view, no client method, and no
other module that reads it. It came from the original specification and was
built; the product never grew a place to put it.

The use case it would serve is "where is the hotel booking?", and
`accommodation` already answers that: it has a `url` field and a 1000-character
`notes` field, both editable, both shown. A second way to attach a booking
reference is not simplicity.

Dead code is not free. It is in the API surface a reader has to understand, in
the roles table, in the data model doc, and in every future "should this go
through attachments?" conversation.

## How it works

Delete `internal/attachments`, its three routes and handlers, the `Attachments`
fields in `api.Server` and `testsupport.App`, its construction in `serve.go`,
and the section in `docs/api.md` and `docs/data-model.md`.

**Leave the `attachments` table.** A shipped migration is never edited, so
removing it means a new migration that drops a table — irreversible, for a
table that holds nothing and costs nothing. Leave it and note in
`docs/product-decisions.md` that the table outlives the code deliberately. If
attachments ever come back the table is still the right shape.

## What it deliberately does not do

- It does not decide against file attachments forever. It removes an
  implementation nothing uses. If a real need appears — a GPX track on an
  event is the plausible one — it comes back with a screen attached this time.
- It does not touch `accommodation.url` or `notes`.

## Acceptance criteria

- [ ] `task check` passes with the package gone.
- [ ] `GET /api/v1/trips/{id}/attachments` returns 404 (no route), not 500.
- [ ] No reference to `attachments` remains outside `migrations/` and the one
      product-decisions note.
- [ ] The Mini App and the bot are untouched — they never called it.

## Existing rules it touches

**A shipped migration is never edited**, which is why the table stays. Nothing
else: the module has no callers to break.

## Open questions

Whether the operator would rather keep the module on the chance a use appears.
The counter-argument is that it has been unused since it was written, and a
module with no caller has no tested behaviour to preserve.
