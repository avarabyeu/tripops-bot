package test

import (
	"strings"
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/attention"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// endedTrip builds a trip that finished yesterday, with two members who have
// both joined, and returns both accesses.
func endedTrip(t *testing.T, app *testsupport.App, seed int64, now time.Time) (trips.Access, trips.Access) {
	t.Helper()
	ctx := t.Context()

	owner := mustUser(t, app, seed, "Anna")
	friend := mustUser(t, app, seed+1, "Peter")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(now.AddDate(0, 0, -3), time.UTC),
		EndDate:   core.DateOf(now.AddDate(0, 0, -1), time.UTC),
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

// TestAttentionOutstandingBalance covers the one rule that fires after a trip
// rather than before it. Every other rule is about getting to the start line
// and goes quiet exactly when the money question appears.
func TestAttentionOutstandingBalance(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	app.Attention.WithClock(func() time.Time { return now })

	anna, peter := endedTrip(t, app, 9101, now)

	// Nobody owes anything yet.
	if _, ok := find(app.Attention.Evaluate(ctx, anna), attention.TypeBalanceOutstanding); ok {
		t.Error("a trip with no expenses should say nothing about money")
	}

	// Anna pays 90.00 for the room, split evenly.
	if _, err := app.Expenses.Create(ctx, anna, expenses.Input{
		Title: "Hotel", Amount: 9000, Category: expenses.CategoryAccommodation,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	// Peter owes his half, and it is his to deal with.
	item, ok := find(app.Attention.Evaluate(ctx, peter), attention.TypeBalanceOutstanding)
	if !ok {
		t.Fatal("peter owes 45.00 and was told nothing")
	}
	if item.Severity != attention.SeverityWarning || !item.Mine {
		t.Errorf("peter's item = %+v, want a warning marked mine", item)
	}
	if !strings.Contains(item.Title, "45.00") {
		t.Errorf("peter's item does not name the amount: %q", item.Title)
	}
	if item.Target.Kind != "balances" {
		t.Errorf("the item points at %q, not the balances screen", item.Target.Kind)
	}

	// Anna is owed it. Worth knowing, not a task — so info, not a warning.
	item, ok = find(app.Attention.Evaluate(ctx, anna), attention.TypeBalanceOutstanding)
	if !ok {
		t.Fatal("anna is owed 45.00 and was told nothing")
	}
	if item.Severity != attention.SeverityInfo {
		t.Errorf("being owed money is %q, want info", item.Severity)
	}

	// Once Peter pays, neither of them hears about it again.
	if _, err := app.Expenses.RecordSettlement(ctx, peter, expenses.SettlementInput{
		From: peter.Member.ID, To: anna.Member.ID, Amount: 4500,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	for name, access := range map[string]trips.Access{"peter": peter, "anna": anna} {
		if _, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeBalanceOutstanding); ok {
			t.Errorf("%s is square but still being told about money", name)
		}
	}
}

// TestAttentionBalanceWaitsForTheTripToEnd: balances swing around during a
// trip as people pay for things, and nagging about a debt still being accrued
// is noise.
func TestAttentionBalanceWaitsForTheTripToEnd(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	app.Attention.WithClock(func() time.Time { return now })

	// A trip that is still running: it ends tomorrow.
	owner := mustUser(t, app, 9201, "Anna")
	friend := mustUser(t, app, 9202, "Peter")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(now.AddDate(0, 0, -1), time.UTC),
		EndDate:   core.DateOf(now.AddDate(0, 0, 1), time.UTC),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	anna := mustAccess(t, app, trip.ID, owner.ID)
	invite, err := app.Trips.CreateInvite(ctx, anna, trips.InviteInput{})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if _, _, err := app.Trips.Join(ctx, friend, invite.Token); err != nil {
		t.Fatalf("join: %v", err)
	}
	peter := mustAccess(t, app, trip.ID, friend.ID)

	if _, err := app.Expenses.Create(ctx, anna, expenses.Input{
		Title: "Hotel", Amount: 9000, Category: expenses.CategoryAccommodation,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, ok := find(app.Attention.Evaluate(ctx, peter), attention.TypeBalanceOutstanding); ok {
		t.Error("the trip is still running; the ledger is not final yet")
	}
}

// TestSchedulerSettleUpRemindsOnce is the dedupe-key test. The scheduler ticks
// every minute; a debt chaser that fires on every pass is the fastest way to
// get a bot muted.
func TestSchedulerSettleUpRemindsOnce(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	// 10:00 local, past the hour the nudge waits for.
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)

	anna, peter := endedTrip(t, app, 9301, now)
	if _, err := app.Expenses.Create(ctx, anna, expenses.Input{
		Title: "Hotel", Amount: 9000, Category: expenses.CategoryAccommodation,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	app.Scheduler.WithClock(func() time.Time { return now })
	app.Scheduler.Tick(ctx)
	app.Scheduler.Tick(ctx)
	app.Scheduler.Tick(ctx)

	for name, access := range map[string]trips.Access{"peter": peter, "anna": anna} {
		if got := scheduled(outbox(t, app, access.User.ID), "settlement_reminder"); got != 1 {
			t.Errorf("%s was nudged %d times, want exactly 1", name, got)
		}
	}
	// The debtor is told who to pay, the creditor who owes them.
	if got := mentioning(outbox(t, app, peter.User.ID), "You owe Anna"); got != 1 {
		t.Errorf("peter's message does not name Anna: %+v", outbox(t, app, peter.User.ID))
	}
	if got := mentioning(outbox(t, app, anna.User.ID), "Peter still owes you"); got != 1 {
		t.Errorf("anna's message does not name Peter: %+v", outbox(t, app, anna.User.ID))
	}
	// It goes out as a reminder, not under `expenses`. That category is the
	// firehose — every bill anybody records — and is off by default, so a
	// nudge sent under it would reach nobody who had not opted in.
	for _, item := range outbox(t, app, peter.User.ID) {
		if item.DedupeKey != nil && strings.HasPrefix(*item.DedupeKey, "settlement_reminder:") &&
			item.Category != notify.CategoryReminders {
			t.Errorf("the nudge is category %q, want reminders", item.Category)
		}
	}
}

// TestSchedulerSettleUpStaysQuiet covers the three cases where nobody should
// hear anything: the trip has not ended, everything is settled, and the trip
// was archived.
func TestSchedulerSettleUpStaysQuiet(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	now := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)
	app.Scheduler.WithClock(func() time.Time { return now })

	// Settled: Peter pays his half before the scheduler ever runs.
	anna, peter := endedTrip(t, app, 9401, now)
	if _, err := app.Expenses.Create(ctx, anna, expenses.Input{
		Title: "Hotel", Amount: 9000, Category: expenses.CategoryAccommodation,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	if _, err := app.Expenses.RecordSettlement(ctx, peter, expenses.SettlementInput{
		From: peter.Member.ID, To: anna.Member.ID, Amount: 4500,
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}

	// Archived, and owing: put away means done with, including the nagging.
	archivedOwner, archivedFriend := endedTrip(t, app, 9411, now)
	if _, err := app.Expenses.Create(ctx, archivedOwner, expenses.Input{
		Title: "Hotel", Amount: 9000, Category: expenses.CategoryAccommodation,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	status := core.TripArchived
	if _, err := app.Trips.Update(ctx, archivedOwner, trips.UpdateInput{Status: &status}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	app.Scheduler.Tick(ctx)

	for name, userID := range map[string]core.ID{
		"peter (settled)":   peter.User.ID,
		"anna (settled)":    anna.User.ID,
		"the archived trip": archivedFriend.User.ID,
	} {
		if got := scheduled(outbox(t, app, userID), "settlement_reminder"); got != 0 {
			t.Errorf("%s got %d settle-up nudges, want none", name, got)
		}
	}
}

// TestSchedulerSettleUpWaitsForMorning: "You owe Anna 45.00" at midnight is a
// worse message than the same one over breakfast, and it is never urgent.
func TestSchedulerSettleUpWaitsForMorning(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	midnight := time.Date(2026, 5, 10, 0, 30, 0, 0, time.UTC)

	anna, peter := endedTrip(t, app, 9501, midnight)
	if _, err := app.Expenses.Create(ctx, anna, expenses.Input{
		Title: "Hotel", Amount: 9000, Category: expenses.CategoryAccommodation,
	}); err != nil {
		t.Fatalf("create expense: %v", err)
	}

	app.Scheduler.WithClock(func() time.Time { return midnight })
	app.Scheduler.Tick(ctx)
	if got := scheduled(outbox(t, app, peter.User.ID), "settlement_reminder"); got != 0 {
		t.Fatalf("nudged at 00:30, want nothing until %02d:00", 9)
	}

	app.Scheduler.WithClock(func() time.Time { return midnight.Add(9 * time.Hour) })
	app.Scheduler.Tick(ctx)
	if got := scheduled(outbox(t, app, peter.User.ID), "settlement_reminder"); got != 1 {
		t.Errorf("nudged %d times after breakfast, want 1", got)
	}
}
