# Attach a receipt, a booking, or the link to the album

*Replaces an earlier proposal to delete the attachments module. That analysis
was right that nothing reaches it and wrong about what to do: unfinished is a
reason to finish or to delete, and this is worth finishing.*

## The question this answers

- "What was that €90 actually for?"
- "Where are the photos from Saturday?"
- "Has anyone got the hotel confirmation?"

All three are asked in the group chat today, and all three are answered by
scrolling back through it — which is the thing this product exists to stop.

## Who and when

Two different moments, which is why this is two stages:

- **A link, any time.** Somebody drops the shared album, the route on a mapping
  site, or the event's registration page, and the group stops re-asking for it.
- **A receipt, during and just after the trip.** Whoever recorded an expense
  photographs the bill. It is the ledger's paper trail, and it matters most in
  the week afterwards when somebody queries an amount.

## Why now

More of this is built than it looks. `internal/attachments` already has:

- five owner types — trip, event, expense, accommodation, checklist item;
- both payload kinds, with the service enforcing that exactly one of `file_id`
  and `url` is given and a table CHECK making sure the one that matters is
  actually there;
- owner validation scoped to the trip, so you cannot attach across trips;
- uploader-or-organiser deletion;
- three API routes, and `trip_id` cascading on trip delete.

What is missing is four things: any UI at all, `Client.GetFile`, a bot that
notices somebody sent it a photo, and a way to render a stored file.

The reason it stalled is not that it is low value. It is that **the two halves
are not the same feature**, and the harder one has been holding the easy one
hostage. A URL is a text field. A photo needs a conversational flow, a new
Telegram API call and a proxy route — because only the bot can mint a
`file_id`, and the URL that downloads one embeds the bot token.

Split them and the link half ships in an afternoon.

## How it works

### Stage 1 — links

No new machinery. The service already accepts a URL and validates the scheme.

- **`caption` becomes required for a link**, and is its label. A bare URL is
  unreadable in a list, and "Photos from Saturday" is the whole point.
- **Two places, not five.**
  - *The trip*: a single line under the trip home header — "🔗 Photos from
    Saturday · Route on Komoot" — **rendered only when something is attached.**
    Nothing to show costs nothing, which is how this stays compatible with
    understanding the trip in ten seconds.
  - *An expense*: a Links section in the expense sheet, next to the split.
- Adding and removing already have the permissions they should have.

### Stage 2 — photos

1. **The bot notices files.** `Message.Photo` and `Message.Document` are
   already on the wire type and nothing reads them. Keep the largest
   `PhotoSize`; its fields map one-to-one onto `attachments.Input`, which is
   what the module was shaped for.
2. **A draft step.** Send a photo to the bot, get "What is this for?" with the
   trip's last few expenses as buttons. `draftStore` already does exactly this
   for `/newtrip`. It is in memory, so a restart loses a half-finished attach —
   fine for a thirty-second flow, and worth knowing rather than discovering.
3. **`Client.GetFile`.** The Telegram client does not have it; resolving a
   `file_id` into something downloadable is the one call this needs and the one
   call it cannot make.
4. **A content route**, `GET /trips/{tripID}/attachments/{attachmentID}/content`,
   which resolves the file server-side and streams it. The download URL
   Telegram returns contains the bot token and must never reach a browser.
   Note this is the **first non-JSON response in the API**: `httpx` has
   exactly two response helpers, `JSON` and `NoContent`, so this route will not
   look like its neighbours and should say why in a comment.

### Both stages: the orphan

**Deleting an owner has to delete its attachments.** `attachments.trip_id`
cascades, so deleting a trip is already safe — but `owner_id` has no foreign
key and cannot have one, being polymorphic. Deleting an expense today would
leave its receipt behind as a row nothing can reach. Whichever stage ships
first owns this, and it needs a test per owner type that is actually exposed.

## What it deliberately does not do

- **No upload from the Mini App.** TripOps stores handles, not bytes. An upload
  endpoint means storage, a size budget, a backup story and a deletion story,
  and the bot path gets the same result with Telegram paying for all four.
- **Not on every owner type.** The model allows five; expose two. Accommodation
  already has `url` and `notes` — a second place to put a booking link is worse
  than one place. Events and checklist items have no question anybody asks.
- **No gallery.** A labelled list, not a grid of thumbnails, not reordering,
  not albums of our own. The album lives wherever the group already keeps it.
- **No reading the receipt.** No OCR, no pulling an amount off a photo to
  compare with the expense. That is a different product.
- **No dashboard tile.** Seven is already a lot.

## Acceptance criteria

Stage 1:

- [ ] A member attaches a labelled link to the trip; every other member sees it
      on the trip home.
- [ ] A link with no label is rejected with a field error.
- [ ] A `javascript:` or `ftp:` URL is rejected — already true, keep it tested.
- [ ] A trip with no attachments renders *no* extra element on the home screen.
- [ ] The person who attached it can remove it; an organiser can remove
      anyone's; a third member gets 403.
- [ ] Deleting an expense deletes its links, leaving no orphan rows.

Stage 2:

- [ ] A photo sent to the bot with no flow in progress gets an explanation, not
      silence.
- [ ] Attaching from the bot stores the largest `PhotoSize`, with its
      dimensions and size.
- [ ] The content route never emits the bot token in a response, a redirect or
      a log line.
- [ ] A non-member requesting the content route gets 404, like every other
      trip-scoped route.
- [ ] An attachment whose `file_id` Telegram no longer resolves renders as a
      missing file, not a broken image and not a 500.

## Existing rules it touches

**Authorization is `trips.Access`** — the existing routes are already inside
the subrouter and the content route must join them, 404 for non-members
included. **Two engines** — no new SQL shapes; `idx_attachments_owner` and
`idx_attachments_trip_id` already cover both reads. **Storage is not ours** —
every stored photo depends on Telegram continuing to resolve its `file_id`, and
the UI has to be honest on the day one does not. And the orphan problem above,
which is the only correctness bug in this request.

## Open questions

1. **Telegram's current limits** for `getFile`: the maximum downloadable size
   and how long the returned path stays valid. Both are documented and both
   have changed before — check the docs at implementation time rather than
   trusting a number written here.
2. **Whether the content route caches.** Suggest not, initially: a handful of
   receipts per trip, and a cache is a storage story arriving through the back
   door.
3. **Whether a receipt is visible to people not sharing that expense.** Suggest
   yes — same trip, same ledger, and the alternative is a per-attachment
   visibility rule nobody asked for.
