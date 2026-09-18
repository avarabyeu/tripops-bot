package test

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// TestTripStatusFollowsTheDates covers the state machine the product is named
// after actually changing state. Before this, `active` and `completed` existed
// in the enum and nothing ever wrote them, so a trip ridden three months ago
// still said "planning" in the trip list.
func TestTripStatusFollowsTheDates(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	owner := mustUser(t, app, 8901, "AV")

	// Everything is measured against one fixed instant so the test does not
	// change meaning at midnight.
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	day := func(offset int) core.Date { return core.DateOf(now.AddDate(0, 0, offset), time.UTC) }

	makeTrip := func(title string, startOffset, endOffset int, timezone string) trips.Trip {
		t.Helper()
		trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
			Title: title, StartDate: day(startOffset), EndDate: day(endOffset), Timezone: timezone,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		if trip.Status != core.TripPlanning {
			t.Fatalf("%s was created as %q, want planning", title, trip.Status)
		}
		return trip
	}
	statusOf := func(id core.ID) core.TripStatus {
		t.Helper()
		trip, err := app.Trips.Get(ctx, id, owner.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return trip.Status
	}

	future := makeTrip("Next month", 20, 21, "UTC")
	starting := makeTrip("Starts today", 0, 1, "UTC")
	running := makeTrip("Half way", -1, 1, "UTC")
	finished := makeTrip("Last weekend", -4, -2, "UTC")
	// Ends today: still active, because the last day counts.
	endingToday := makeTrip("Ends today", -2, 0, "UTC")
	archived := makeTrip("Put away", -10, -9, "UTC")

	ownerAccess := mustAccess(t, app, archived.ID, owner.ID)
	status := core.TripArchived
	if _, err := app.Trips.Update(ctx, ownerAccess, trips.UpdateInput{Status: &status}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	changed, err := app.Trips.AdvanceStatuses(ctx, now)
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if changed != 4 {
		t.Errorf("advanced %d trips, want 4 (starts today, half way, last weekend, ends today)", changed)
	}

	for _, want := range []struct {
		name   string
		id     core.ID
		status core.TripStatus
	}{
		{"a trip starting in three weeks", future.ID, core.TripPlanning},
		{"a trip starting today", starting.ID, core.TripActive},
		{"a trip half way through", running.ID, core.TripActive},
		{"a trip ending today", endingToday.ID, core.TripActive},
		{"a trip that ended on the weekend", finished.ID, core.TripCompleted},
		{"a trip somebody archived", archived.ID, core.TripArchived},
	} {
		if got := statusOf(want.id); got != want.status {
			t.Errorf("%s is %q, want %q", want.name, got, want.status)
		}
	}

	// Running again writes nothing: the scheduler ticks every minute and this
	// must not be a write per tick.
	again, err := app.Trips.AdvanceStatuses(ctx, now)
	if err != nil {
		t.Fatalf("advance twice: %v", err)
	}
	if again != 0 {
		t.Errorf("a second pass over unchanged trips wrote %d rows", again)
	}

	// A completed trip is not a closed one. People add the last expenses after
	// they get home, so `Writable()` still means "not archived".
	access := mustAccess(t, app, finished.ID, owner.ID)
	if _, err := app.Expenses.Create(ctx, access, expenses.Input{
		Title: "Fuel on the way back", Amount: 3000, Category: expenses.CategoryFuel,
	}); err != nil {
		t.Errorf("a completed trip should still accept an expense: %v", err)
	}
}

// TestTripStatusUsesTheTripTimezone: doing the comparison in UTC gets it wrong
// by a day for anyone far enough east or west, which is the whole reason the
// decision is made in Go rather than in SQL.
func TestTripStatusUsesTheTripTimezone(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()
	owner := mustUser(t, app, 8911, "AV")

	// 23:00 UTC on 1 May is already 11:00 on 2 May in Auckland.
	now := time.Date(2026, 5, 1, 23, 0, 0, 0, time.UTC)
	start := core.Date{Year: 2026, Month: 5, Day: 2}

	auckland, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title: "Auckland", StartDate: start, EndDate: start, Timezone: "Pacific/Auckland",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	utc, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title: "London", StartDate: start, EndDate: start, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := app.Trips.AdvanceStatuses(ctx, now); err != nil {
		t.Fatalf("advance: %v", err)
	}

	reload := func(id core.ID) core.TripStatus {
		t.Helper()
		trip, err := app.Trips.Get(ctx, id, owner.ID)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return trip.Status
	}
	if got := reload(auckland.ID); got != core.TripActive {
		t.Errorf("in Auckland it is already the 2nd, so the trip is %q, want active", got)
	}
	if got := reload(utc.ID); got != core.TripPlanning {
		t.Errorf("in UTC it is still the 1st, so the trip is %q, want planning", got)
	}
}
