package test

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/attention"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// find returns the attention item of a given type, if the engine produced one.
func find(items []attention.Item, kind attention.Type) (attention.Item, bool) {
	for _, item := range items {
		if item.Type == kind {
			return item, true
		}
	}
	return attention.Item{}, false
}

func types(items []attention.Item) []attention.Type {
	out := make([]attention.Type, 0, len(items))
	for _, item := range items {
		out = append(out, item.Type)
	}
	return out
}

// TestAttentionRules builds a trip with one of everything wrong and checks the
// engine says so. These are the sentences the dashboard leads with, so each
// rule is asserted by name rather than by counting items.
func TestAttentionRules(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner := mustUser(t, app, 8101, "AV")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(time.Now().AddDate(0, 0, 5), time.UTC),
		EndDate:   core.DateOf(time.Now().AddDate(0, 0, 6), time.UTC),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	access := mustAccess(t, app, trip.ID, owner.ID)

	// A trip that spans a night with nowhere to sleep, and no departure.
	items := app.Attention.Evaluate(ctx, access)
	if _, ok := find(items, attention.TypeNoDeparture); !ok {
		t.Errorf("expected a missing-departure warning, got %v", types(items))
	}
	if _, ok := find(items, attention.TypeNoAccommodation); !ok {
		t.Errorf("expected a missing-accommodation warning, got %v", types(items))
	}

	// Add both, and the two warnings must go away.
	if _, err := app.Events.Create(ctx, access, events.CreateInput{
		Title: "Departure", Type: events.TypeDeparture,
		StartAt: time.Now().Add(120 * time.Hour),
	}); err != nil {
		t.Fatalf("create departure: %v", err)
	}
	place, err := app.Accommodation.Create(ctx, access, accommodation.Input{
		Name: "Apartment B", Capacity: 4,
		GuestIDs: []core.ID{access.Member.ID},
	})
	if err != nil {
		t.Fatalf("create accommodation: %v", err)
	}

	items = app.Attention.Evaluate(ctx, access)
	if _, ok := find(items, attention.TypeNoDeparture); ok {
		t.Error("the departure warning should be gone once a departure exists")
	}
	if _, ok := find(items, attention.TypeNoAccommodation); ok {
		t.Error("the accommodation warning should be gone once a place exists")
	}

	// The owner is now a guest who has not confirmed, and it is their own
	// problem, so the item must be marked as theirs.
	pending, ok := find(items, attention.TypeAccommodationPending)
	if !ok {
		t.Fatalf("expected an unconfirmed-bed warning, got %v", types(items))
	}
	if !pending.Mine {
		t.Error("the caller's own unconfirmed bed must be marked as theirs")
	}
	if pending.Title != "You have not confirmed accommodation" {
		t.Errorf("title = %q; it should address the reader directly", pending.Title)
	}
	if _, err := app.Accommodation.SetGuestStatus(ctx, access, place.ID, core.Nil,
		accommodation.GuestConfirmed); err != nil {
		t.Fatalf("confirm bed: %v", err)
	}
	if _, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeAccommodationPending); ok {
		t.Error("confirming the bed should clear the warning")
	}
}

// TestAttentionUndecidedAndVotes covers the two rules that chase people for an
// answer, including the escalation as a deadline approaches.
func TestAttentionUndecidedAndVotes(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner := mustUser(t, app, 8201, "AV")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(time.Now().AddDate(0, 0, 1), time.UTC),
		EndDate:   core.DateOf(time.Now().AddDate(0, 0, 1), time.UTC),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	access := mustAccess(t, app, trip.ID, owner.ID)

	// An event tomorrow that the caller has not answered about.
	if _, err := app.Events.Create(ctx, access, events.CreateInput{
		Title: "Departure", Type: events.TypeDeparture,
		StartAt: time.Now().Add(20 * time.Hour),
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}
	item, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeEventUndecided)
	if !ok {
		t.Fatal("expected an undecided-RSVP warning")
	}
	if !item.Mine {
		t.Error("the caller's own missing answer must be marked as theirs")
	}
	// Inside 48 hours it is a warning, not a note.
	if item.Severity != attention.SeverityWarning {
		t.Errorf("severity = %s, want warning for an event within 48 hours", item.Severity)
	}

	// A decision closing within a day, not yet voted on, is critical.
	soon := time.Now().Add(3 * time.Hour)
	decision, err := app.Decisions.Create(ctx, access, decisions.CreateInput{
		Title: "Where do we start?", Options: []string{"A", "B"}, Deadline: &soon,
	})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	vote, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeDecisionVotePending)
	if !ok {
		t.Fatal("expected a pending-vote warning")
	}
	if vote.Severity != attention.SeverityCritical {
		t.Errorf("severity = %s, want critical within 24 hours of the deadline", vote.Severity)
	}

	// Voting clears it; closing without resolving then asks the organiser for
	// the outcome, which is where a group silently loses a decision.
	if _, err := app.Decisions.Vote(ctx, access, decision.ID, decision.Options[0].ID); err != nil {
		t.Fatalf("vote: %v", err)
	}
	if _, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeDecisionVotePending); ok {
		t.Error("voting should clear the pending-vote warning")
	}
	if _, err := app.Decisions.Close(ctx, access, decision.ID); err != nil {
		t.Fatalf("close decision: %v", err)
	}
	if _, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeDecisionNeedsOutcome); !ok {
		t.Error("a closed but unresolved decision should ask the organiser to confirm it")
	}
}

// TestAttentionCapacityAndSeats covers the logistics rules, including the
// overbooked state that only stale data can produce.
func TestAttentionCapacityAndSeats(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner := mustUser(t, app, 8301, "AV")
	friend := mustUser(t, app, 8302, "Vasya")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(time.Now().AddDate(0, 0, 3), time.UTC),
		EndDate:   core.DateOf(time.Now().AddDate(0, 0, 3), time.UTC),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	access := mustAccess(t, app, trip.ID, owner.ID)
	invite, err := app.Trips.CreateInvite(ctx, access, trips.InviteInput{})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, _, err := app.Trips.Join(ctx, friend, invite.Token); err != nil {
		t.Fatalf("join: %v", err)
	}
	friendAccess := mustAccess(t, app, trip.ID, friend.ID)

	// One car, the friend driving, nobody else seated yet.
	vehicle, err := app.Logistics.Create(ctx, access, logistics.VehicleInput{
		Name: "Car 1", Capacity: 2, DriverMemberID: friendAccess.Member.ID,
	})
	if err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	seat, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeNoSeat)
	if !ok {
		t.Fatalf("the owner has no seat and should be told")
	}
	if !seat.Mine {
		t.Error("a missing seat is the reader's own problem")
	}
	if _, err := app.Logistics.Join(ctx, access, vehicle.ID); err != nil {
		t.Fatalf("take a seat: %v", err)
	}
	if _, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeNoSeat); ok {
		t.Error("taking a seat should clear the warning")
	}

	// Overbooking cannot be reached through the service — it refuses. It can
	// exist in data written before a rule, so the rule still has to catch it.
	if err := app.DB.Exec("UPDATE vehicles SET capacity = 1 WHERE id = ?", vehicle.ID).Error; err != nil {
		t.Fatalf("shrink the car behind the service's back: %v", err)
	}
	over, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeVehicleOverbooked)
	if !ok {
		t.Fatal("an overbooked vehicle must be reported")
	}
	if over.Severity != attention.SeverityCritical {
		t.Errorf("severity = %s, want critical", over.Severity)
	}
}

// TestAttentionOrdering: whatever else is wrong, what the reader has to do
// personally comes first, and critical beats warning beats info.
func TestAttentionOrdering(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner := mustUser(t, app, 8401, "AV")
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet",
		StartDate: core.DateOf(time.Now().AddDate(0, 0, 2), time.UTC),
		EndDate:   core.DateOf(time.Now().AddDate(0, 0, 3), time.UTC),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	access := mustAccess(t, app, trip.ID, owner.ID)

	// Something of the reader's own: an assigned checklist item.
	list, err := app.Checklists.CreateList(ctx, access, checklists.ListInput{
		Title: "Bike", Scope: checklists.ScopeShared, Items: []string{"Front light"},
	})
	if err != nil {
		t.Fatalf("create checklist: %v", err)
	}
	if _, err := app.Checklists.UpdateItem(ctx, access, list.Items[0].ID, checklists.ItemPatch{
		AssignedTo: &access.Member.ID,
	}); err != nil {
		t.Fatalf("assign item: %v", err)
	}

	items := app.Attention.Evaluate(ctx, access)
	if len(items) < 2 {
		t.Fatalf("expected several items, got %v", types(items))
	}
	if !items[0].Mine {
		t.Errorf("the reader's own items must sort first, got %v", types(items))
	}
	if _, ok := find(items, attention.TypeChecklistAssigned); !ok {
		t.Errorf("expected an assigned-checklist warning, got %v", types(items))
	}

	// Severity must be non-increasing within each group.
	rank := map[attention.Severity]int{
		attention.SeverityCritical: 3, attention.SeverityWarning: 2, attention.SeverityInfo: 1,
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Mine == items[i].Mine && rank[items[i-1].Severity] < rank[items[i].Severity] {
			t.Errorf("item %d (%s) outranks the one before it (%s)",
				i, items[i].Severity, items[i-1].Severity)
		}
	}

	// Completing the item removes it.
	done := true
	if _, err := app.Checklists.UpdateItem(ctx, access, list.Items[0].ID,
		checklists.ItemPatch{Completed: &done}); err != nil {
		t.Fatalf("complete item: %v", err)
	}
	if _, ok := find(app.Attention.Evaluate(ctx, access), attention.TypeChecklistAssigned); ok {
		t.Error("completing the item should clear the warning")
	}
}
