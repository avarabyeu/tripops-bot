package test

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/attachments"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// TestAttachmentsBelongToTheirTrip is the rule that matters here: an
// attachment can only be hung off an object of the trip it is being added to.
// Without the check, a member of one trip could attach to another trip's
// expense by guessing an id.
func TestAttachmentsBelongToTheirTrip(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner := mustUser(t, app, 8601, "AV")
	mine, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title: "My trip", StartDate: core.NewDate(2026, time.May, 1), EndDate: core.NewDate(2026, time.May, 2),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	myAccess := mustAccess(t, app, mine.ID, owner.ID)

	stranger := mustUser(t, app, 8602, "Someone else")
	theirs, err := app.Trips.Create(ctx, stranger, trips.CreateInput{
		Title: "Their trip", StartDate: core.NewDate(2026, time.May, 1), EndDate: core.NewDate(2026, time.May, 2),
	})
	if err != nil {
		t.Fatalf("create other trip: %v", err)
	}
	theirAccess := mustAccess(t, app, theirs.ID, stranger.ID)
	theirEvent, err := app.Events.Create(ctx, theirAccess, events.CreateInput{
		Title: "Their departure", Type: events.TypeDeparture, StartAt: time.Now().Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create their event: %v", err)
	}

	// Attaching to an object of somebody else's trip must not be possible,
	// even with a valid id in hand.
	_, err = app.Attachments.Add(ctx, myAccess, attachments.Input{
		OwnerType: attachments.OwnerEvent,
		OwnerID:   theirEvent.ID,
		URL:       "https://example.com/route.gpx",
	})
	if err == nil {
		t.Fatal("attached to another trip's event")
	}
	if code := core.CodeOf(err); code != core.CodeNotFound {
		t.Errorf("code = %s, want not_found", code)
	}

	// The same call against an object of my own trip works.
	myEvent, err := app.Events.Create(ctx, myAccess, events.CreateInput{
		Title: "My departure", Type: events.TypeDeparture, StartAt: time.Now().Add(48 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create my event: %v", err)
	}
	attachment, err := app.Attachments.Add(ctx, myAccess, attachments.Input{
		OwnerType: attachments.OwnerEvent,
		OwnerID:   myEvent.ID,
		URL:       "https://example.com/route.gpx",
		Caption:   "The route",
	})
	if err != nil {
		t.Fatalf("attach to my own event: %v", err)
	}
	if attachment.Kind != attachments.KindURL {
		t.Errorf("kind = %q, want url", attachment.Kind)
	}

	listed, err := app.Attachments.List(ctx, myAccess, attachments.OwnerEvent, myEvent.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != attachment.ID {
		t.Fatalf("listed %d attachments, want the one just added", len(listed))
	}
	// The other trip's owner must not see it, and asking is not an error —
	// there is simply nothing of theirs there.
	otherList, err := app.Attachments.List(ctx, theirAccess, attachments.OwnerEvent, myEvent.ID)
	if err != nil {
		t.Fatalf("list from the other trip: %v", err)
	}
	if len(otherList) != 0 {
		t.Errorf("another trip's member saw %d attachments", len(otherList))
	}
}

// TestAttachmentsAcceptEitherAFileOrALink: Telegram keeps the bytes, so an
// attachment is either a file handle or a URL, never both and never neither.
func TestAttachmentsAcceptEitherAFileOrALink(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner := mustUser(t, app, 8701, "AV")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title: "Trip", StartDate: core.NewDate(2026, time.May, 1), EndDate: core.NewDate(2026, time.May, 2),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	access := mustAccess(t, app, trip.ID, owner.ID)

	expense, err := app.Expenses.Create(ctx, access, expenses.Input{
		Title: "Fuel", Amount: 5000, Category: expenses.CategoryFuel,
	})
	if err != nil {
		t.Fatalf("create expense: %v", err)
	}

	cases := map[string]attachments.Input{
		"neither a file nor a link": {OwnerType: attachments.OwnerExpense, OwnerID: expense.ID},
		"both at once": {
			OwnerType: attachments.OwnerExpense, OwnerID: expense.ID,
			FileID: "AgACAgIAAx", URL: "https://example.com/receipt.jpg",
		},
		"a link that is not http": {
			OwnerType: attachments.OwnerExpense, OwnerID: expense.ID,
			URL: "javascript:alert(1)",
		},
		"no owner": {OwnerType: attachments.OwnerExpense, URL: "https://example.com/x.jpg"},
		"unknown owner type": {
			OwnerType: attachments.OwnerType("spaceship"), OwnerID: expense.ID,
			URL: "https://example.com/x.jpg",
		},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := app.Attachments.Add(ctx, access, in); err == nil {
				t.Error("expected the attachment to be rejected")
			} else if code := core.CodeOf(err); code != core.CodeInvalid {
				t.Errorf("code = %s, want invalid", code)
			}
		})
	}

	// A Telegram photo: we keep the handle, not the bytes.
	photo, err := app.Attachments.Add(ctx, access, attachments.Input{
		OwnerType: attachments.OwnerExpense,
		OwnerID:   expense.ID,
		FileID:    "AgACAgIAAxkBAAIB",
		MimeType:  "image/jpeg",
		SizeBytes: 90210,
		Caption:   "Receipt",
	})
	if err != nil {
		t.Fatalf("attach a Telegram photo: %v", err)
	}
	if photo.Kind != attachments.KindTelegramFile {
		t.Errorf("kind = %q, want telegram_file", photo.Kind)
	}
	if photo.URL != "" {
		t.Error("a Telegram file must not carry a URL")
	}
}

// TestAttachmentDeletionPermissions: the person who attached something may
// remove it, an organiser may remove anything, and a bystander may not.
func TestAttachmentDeletionPermissions(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, friend := party(t, app, 8801)

	attachment, err := app.Attachments.Add(ctx, friend, attachments.Input{
		OwnerType: attachments.OwnerTrip,
		OwnerID:   friend.Trip.ID,
		URL:       "https://example.com/plan.pdf",
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	// A third member, neither the uploader nor an organiser.
	bystander := mustUser(t, app, 8803, "Peter")
	invite, err := app.Trips.CreateInvite(ctx, owner, trips.InviteInput{})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, _, err := app.Trips.Join(ctx, bystander, invite.Token); err != nil {
		t.Fatalf("join: %v", err)
	}
	bystanderAccess := mustAccess(t, app, owner.Trip.ID, bystander.ID)

	if err := app.Attachments.Delete(ctx, bystanderAccess, attachment.ID); err == nil {
		t.Error("a bystander deleted somebody else's attachment")
	} else if code := core.CodeOf(err); code != core.CodeForbidden {
		t.Errorf("code = %s, want forbidden", code)
	}

	// The organiser can.
	if err := app.Attachments.Delete(ctx, owner, attachment.ID); err != nil {
		t.Fatalf("the organiser could not delete it: %v", err)
	}
	if err := app.Attachments.Delete(ctx, owner, attachment.ID); err == nil {
		t.Error("deleting the same attachment twice should report it is gone")
	}

	// And the uploader can remove their own.
	own, err := app.Attachments.Add(ctx, friend, attachments.Input{
		OwnerType: attachments.OwnerTrip, OwnerID: friend.Trip.ID,
		URL: "https://example.com/other.pdf",
	})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := app.Attachments.Delete(ctx, friend, own.ID); err != nil {
		t.Errorf("the uploader could not delete their own attachment: %v", err)
	}
}
