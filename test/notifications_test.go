package test

import (
	"strings"
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// outbox reads the queued notifications for one user, newest last. Tests read
// the table directly rather than draining it, so a single scenario can assert
// on the queue more than once.
func outbox(t *testing.T, app *testsupport.App, userID core.ID) []notify.Notification {
	t.Helper()
	var rows []notify.Notification
	err := app.DB.WithContext(t.Context()).
		Where("user_id = ?", userID).
		Order("created_at, id").
		Find(&rows).Error
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return rows
}

func countCategory(items []notify.Notification, category notify.Category) int {
	n := 0
	for _, item := range items {
		if item.Category == category {
			n++
		}
	}
	return n
}

// party sets up a trip with two joined members and returns both accesses.
func party(t *testing.T, app *testsupport.App, seed int64) (trips.Access, trips.Access) {
	t.Helper()
	ctx := t.Context()

	owner := mustUser(t, app, seed, "AV")
	friend := mustUser(t, app, seed+1, "Vasya")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(time.Now().AddDate(0, 0, 1), time.UTC),
		EndDate:   core.DateOf(time.Now().AddDate(0, 0, 2), time.UTC),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	ownerAccess := mustAccess(t, app, trip.ID, owner.ID)

	invite, err := app.Trips.CreateInvite(ctx, ownerAccess, trips.InviteInput{})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, _, err := app.Trips.Join(ctx, friend, invite.Token); err != nil {
		t.Fatalf("join: %v", err)
	}
	return ownerAccess, mustAccess(t, app, trip.ID, friend.ID)
}

// TestNotificationPreferencesGateTheOutbox: a muted category never reaches the
// table at all, which is what keeps the product from becoming noisy.
func TestNotificationPreferencesGateTheOutbox(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, friend := party(t, app, 8501)

	// Joining is a trip update, on by default, and reaches everyone but the
	// person who joined.
	if got := countCategory(outbox(t, app, owner.User.ID), notify.CategoryTripUpdates); got != 1 {
		t.Errorf("owner received %d trip updates about the new member, want 1", got)
	}
	if got := len(outbox(t, app, friend.User.ID)); got != 0 {
		t.Errorf("the person who joined was notified about themselves %d times", got)
	}

	// Expenses are off by default: recording one notifies nobody.
	if _, err := app.Expenses.Create(ctx, owner, expenses.Input{
		Title: "Fuel", Amount: 5000, Category: expenses.CategoryFuel,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if got := countCategory(outbox(t, app, friend.User.ID), notify.CategoryExpenses); got != 0 {
		t.Errorf("expense notifications are off by default, but %d were queued", got)
	}

	// Turned on, the next one arrives.
	if _, err := app.Notify.SavePreferences(ctx, friend.User.ID, notify.Preferences{
		TripUpdates: true, Decisions: true, Reminders: true, Checklist: true, Expenses: true,
	}); err != nil {
		t.Fatalf("save preferences: %v", err)
	}
	if _, err := app.Expenses.Create(ctx, owner, expenses.Input{
		Title: "Parking", Amount: 1000, Category: expenses.CategoryParking,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if got := countCategory(outbox(t, app, friend.User.ID), notify.CategoryExpenses); got != 1 {
		t.Errorf("after opting in, %d expense notifications were queued, want 1", got)
	}

	// Muting trip updates stops them for the next event too.
	if _, err := app.Notify.SavePreferences(ctx, friend.User.ID, notify.Preferences{}); err != nil {
		t.Fatalf("save preferences: %v", err)
	}
	before := len(outbox(t, app, friend.User.ID))
	if _, err := app.Events.Create(ctx, owner, events.CreateInput{
		Title: "Departure", Type: events.TypeDeparture, StartAt: time.Now().Add(30 * time.Hour),
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	if after := len(outbox(t, app, friend.User.ID)); after != before {
		t.Errorf("a fully muted user received %d new notifications", after-before)
	}
}

// TestSchedulerRemindsNonVotersOnce is the property that lets the scheduler run
// on a dumb interval: the rule is re-evaluated constantly and stays silent
// after the first ping.
func TestSchedulerRemindsNonVotersOnce(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, friend := party(t, app, 8601)

	deadline := time.Now().Add(90 * time.Minute)
	decision, err := app.Decisions.Create(ctx, owner, decisions.CreateInput{
		Title: "Departure time", Options: []string{"18:00", "19:00"}, Deadline: &deadline,
	})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	// The owner votes; the friend does not.
	if _, err := app.Decisions.Vote(ctx, owner, decision.ID, decision.Options[0].ID); err != nil {
		t.Fatalf("vote: %v", err)
	}

	app.Scheduler.Tick(ctx)

	reminders := scheduled(outbox(t, app, friend.User.ID), "decision_reminder")
	if reminders != 1 {
		t.Fatalf("the non-voter got %d reminders, want 1", reminders)
	}
	if got := scheduled(outbox(t, app, owner.User.ID), "decision_reminder"); got != 0 {
		t.Errorf("somebody who already voted got %d reminders", got)
	}

	// Ticking again must add nothing: the dedupe key already exists.
	app.Scheduler.Tick(ctx)
	app.Scheduler.Tick(ctx)
	if got := scheduled(outbox(t, app, friend.User.ID), "decision_reminder"); got != 1 {
		t.Errorf("after three ticks the non-voter has %d reminders, want 1", got)
	}
}

// TestSchedulerAutoClosesDecisions: a deadline that passes closes voting by
// itself, so nobody has to remember to.
func TestSchedulerAutoClosesDecisions(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, friend := party(t, app, 8701)

	// Created with a future deadline, because the service refuses one in the
	// past; the clock then moves on.
	deadline := time.Now().Add(30 * time.Minute)
	decision, err := app.Decisions.Create(ctx, owner, decisions.CreateInput{
		Title: "Where do we eat?", Options: []string{"Pizza", "Pierogi"}, Deadline: &deadline,
	})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}

	app.Scheduler.WithClock(func() time.Time { return deadline.Add(time.Minute) })
	app.Scheduler.Tick(ctx)

	closed, err := app.Decisions.Get(ctx, owner, decision.ID)
	if err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if closed.Status != decisions.StatusClosed {
		t.Errorf("status = %s, want closed once the deadline passed", closed.Status)
	}
	if closed.ClosedAt == nil {
		t.Error("closed_at was not recorded")
	}
	// Auto-closing is not resolving: the outcome is still the organiser's call.
	if !closed.ResolvedOptionID.IsZero() {
		t.Error("auto-close must never pick a winner")
	}

	if got := scheduled(outbox(t, app, friend.User.ID), "decision_closed"); got != 1 {
		t.Errorf("the group was told %d times that voting closed, want 1", got)
	}
	if got := mentioning(outbox(t, app, friend.User.ID), "Voting closed"); got != 1 {
		t.Errorf("the closure notice does not read as one: %d matches", got)
	}
	app.Scheduler.Tick(ctx)
	if got := scheduled(outbox(t, app, friend.User.ID), "decision_closed"); got != 1 {
		t.Errorf("a second tick re-announced the closure (%d total)", got)
	}
}

// TestSchedulerRemindsAboutTomorrow covers the "📅 Tomorrow" digest.
func TestSchedulerRemindsAboutTomorrow(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, friend := party(t, app, 8801)

	// Well outside the 24 hour window: nothing yet.
	far, err := app.Events.Create(ctx, owner, events.CreateInput{
		Title: "Finish", Type: events.TypeArrival, StartAt: time.Now().Add(40 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	app.Scheduler.Tick(ctx)
	if got := scheduled(outbox(t, app, friend.User.ID), "event_reminder"); got != 0 {
		t.Errorf("an event 40 hours away was reminded about %d times", got)
	}

	// Inside the window it is announced once, to everyone who has not declined.
	if _, err := app.Events.Create(ctx, owner, events.CreateInput{
		Title: "Departure", Type: events.TypeDeparture, StartAt: time.Now().Add(18 * time.Hour),
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	app.Scheduler.Tick(ctx)
	app.Scheduler.Tick(ctx)
	if got := scheduled(outbox(t, app, friend.User.ID), "event_reminder"); got != 1 {
		t.Errorf("the reminder about Departure was queued %d times, want 1", got)
	}

	// Somebody who said they are not coming is not reminded. Moving the clock
	// forward brings the far event inside the window, so the only thing
	// keeping the count flat is the declined RSVP.
	if _, err := app.Events.SetRSVP(ctx, friend, far.ID, core.Nil, events.RSVPNotAttending); err != nil {
		t.Fatalf("decline: %v", err)
	}
	before := scheduled(outbox(t, app, friend.User.ID), "event_reminder")
	app.Scheduler.WithClock(func() time.Time { return time.Now().UTC().Add(20 * time.Hour) })
	app.Scheduler.Tick(ctx)
	if after := scheduled(outbox(t, app, friend.User.ID), "event_reminder"); after != before {
		t.Errorf("somebody who declined got %d new reminders", after-before)
	}
	// The owner, who never declined, does get one.
	if got := scheduled(outbox(t, app, owner.User.ID), "event_reminder"); got == 0 {
		t.Error("the owner should have been reminded about the event now in range")
	}
}

// TestOutboxDeliveryLifecycle exercises what the Telegram worker does, without
// Telegram: claim, deliver, and never deliver the same row twice.
func TestOutboxDeliveryLifecycle(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	// A user who has talked to the bot, so there is somewhere to deliver.
	recipient, err := app.Users.EnsureUser(ctx, users.Identity{
		TelegramID: 8901, FirstName: "Vasya", ChatID: 424242,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := app.Notify.Enqueue(ctx, notify.Notification{
		UserID: recipient.ID, Category: notify.CategoryReminders,
		Title: "📅 Tomorrow", Body: "Departure at 19:00",
		DedupeKey: notify.DedupeKey("test", "one"),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The same dedupe key twice is one row.
	if err := app.Notify.Enqueue(ctx, notify.Notification{
		UserID: recipient.ID, Category: notify.CategoryReminders,
		Title: "📅 Tomorrow", Body: "Departure at 19:00",
		DedupeKey: notify.DedupeKey("test", "one"),
	}); err != nil {
		t.Fatalf("re-enqueue should be a silent no-op: %v", err)
	}
	if got := len(outbox(t, app, recipient.ID)); got != 1 {
		t.Fatalf("outbox holds %d rows, want 1", got)
	}

	claimed, err := app.Notify.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d notifications, want 1", len(claimed))
	}
	// The worker needs the chat id, which lives on the user, not the row.
	if claimed[0].ChatID != 424242 {
		t.Errorf("claimed notification has chat id %d", claimed[0].ChatID)
	}

	// A second worker must not pick up what the first one is sending.
	again, err := app.Notify.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a claimed notification was handed out again (%d rows)", len(again))
	}

	if err := app.Notify.MarkSent(ctx, claimed[0].ID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}
	rows := outbox(t, app, recipient.ID)
	if rows[0].Status != notify.StatusSent {
		t.Errorf("status = %q, want sent", rows[0].Status)
	}
	if rows[0].SentAt == nil {
		t.Error("sent_at was not recorded")
	}

	// Somebody who has never opened a chat with the bot is unreachable; the
	// worker skips rather than retries.
	unreachable, err := app.Users.EnsureUser(ctx, users.Identity{TelegramID: 8902, FirstName: "Ghost"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := app.Notify.Enqueue(ctx, notify.Notification{
		UserID: unreachable.ID, Category: notify.CategoryReminders, Body: "Nobody home",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	pending, err := app.Notify.ClaimDue(ctx, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(pending) != 1 || pending[0].ChatID != 0 {
		t.Fatalf("expected one claim with no chat id, got %+v", pending)
	}
	if err := app.Notify.MarkSkipped(ctx, pending[0].ID, "no private chat with the bot"); err != nil {
		t.Fatalf("mark skipped: %v", err)
	}
	if rows := outbox(t, app, unreachable.ID); rows[0].Status != notify.StatusSkipped {
		t.Errorf("status = %q, want skipped", rows[0].Status)
	}
}

// scheduled counts queued notifications produced by one scheduler rule.
//
// Rules are identified by their dedupe key prefix rather than by their text: a
// creation announcement and a scheduled reminder can mention the same event,
// and only the second one carries a key.
func scheduled(items []notify.Notification, rulePrefix string) int {
	n := 0
	for _, item := range items {
		if item.DedupeKey != nil && strings.HasPrefix(*item.DedupeKey, rulePrefix+":") {
			n++
		}
	}
	return n
}

// mentioning counts queued notifications whose text contains a phrase.
func mentioning(items []notify.Notification, phrase string) int {
	n := 0
	for _, item := range items {
		if strings.Contains(item.Title, phrase) || strings.Contains(item.Body, phrase) {
			n++
		}
	}
	return n
}

// TestInstantsAreStoredInUTC is a regression test.
//
// Clients send times with whatever offset their phone is in. Those instants
// are compared by the storage layer as written — SQLite compares the text, and
// a PostgreSQL `timestamp` column drops the offset without converting — so an
// unnormalised "12:09+02:00" ranges as though it were 12:09 UTC. That silently
// moved every deadline by the client's offset and made the reminder windows
// miss.
func TestInstantsAreStoredInUTC(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, _ := party(t, app, 8951)

	// A deadline 90 minutes away, expressed two hours east of UTC.
	east := time.FixedZone("UTC+2", 2*60*60)
	deadline := time.Now().Add(90 * time.Minute).In(east)
	if _, err := app.Decisions.Create(ctx, owner, decisions.CreateInput{
		Title: "Departure time", Options: []string{"18:00", "19:00"}, Deadline: &deadline,
	}); err != nil {
		t.Fatalf("create decision: %v", err)
	}

	// The window is two hours wide, so a deadline 90 minutes out must fall in
	// it whatever offset the client happened to use.
	now := time.Now().UTC()
	soon, err := app.Decisions.Repo().ClosingSoon(ctx, now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("closing soon: %v", err)
	}
	if len(soon) != 1 {
		t.Fatalf("the reminder window found %d decisions, want 1: an offset is being stored verbatim", len(soon))
	}
	if delta := soon[0].Deadline.Sub(deadline).Abs(); delta > time.Second {
		t.Errorf("deadline came back %v away from what was sent", delta)
	}

	// The same for an event, which the "tomorrow" digest ranges over.
	startAt := time.Now().Add(18 * time.Hour).In(east)
	created, err := app.Events.Create(ctx, owner, events.CreateInput{
		Title: "Departure", Type: events.TypeDeparture, StartAt: startAt,
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if created.StartAt.Location() != time.UTC {
		t.Errorf("event start came back in %v, want UTC", created.StartAt.Location())
	}
	inRange, err := app.Events.Repo().InRange(ctx, owner.Trip.ID, now, now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("in range: %v", err)
	}
	if len(inRange) != 1 {
		t.Errorf("the 24 hour window found %d events, want 1", len(inRange))
	}
}
