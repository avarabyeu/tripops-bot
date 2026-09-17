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
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// TestHappyPath walks the MVP definition of done end to end: a group of
// friends organises a two-day cycling trip through the application and nothing
// else.
//
//	create trip → invite → join → create event → create decision → vote
//	→ create expense → calculate balances → mark settlement
func TestHappyPath(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	// ------------------------------------------------ 1. create the trip --

	av := mustUser(t, app, 9001, "AV")
	trip, err := app.Trips.Create(ctx, av, trips.CreateInput{
		Title:       "Brevet Łódź 200",
		Description: "200 km, one overnight",
		StartDate:   core.NewDate(2026, time.September, 23),
		EndDate:     core.NewDate(2026, time.September, 24),
		Timezone:    "Europe/Warsaw",
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}
	owner := mustAccess(t, app, trip.ID, av.ID)

	// ------------------------------------------- 2. invite and 3. join --

	invite, err := app.Trips.CreateInvite(ctx, owner, trips.InviteInput{Role: core.RoleMember})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if invite.URL == "" {
		t.Error("an invite with no link is useless to the organiser")
	}
	// The deep link carries a random token, never the trip id.
	if wantPrefix := "https://t.me/tripops_test_bot?start=inv_"; invite.URL[:len(wantPrefix)] != wantPrefix {
		t.Errorf("invite URL = %q", invite.URL)
	}

	vasya := mustUser(t, app, 9002, "Vasya")
	peter := mustUser(t, app, 9003, "Peter")
	for _, u := range []users.User{vasya, peter} {
		if _, _, err := app.Trips.Join(ctx, u, invite.Token); err != nil {
			t.Fatalf("%s could not join: %v", u.FirstName, err)
		}
	}
	// Tapping an old link twice must not fail or duplicate the membership.
	if _, _, err := app.Trips.Join(ctx, vasya, invite.Token); err != nil {
		t.Fatalf("re-joining should be a no-op: %v", err)
	}

	members, err := app.Trips.Members(ctx, owner)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("trip has %d members, want 3", len(members))
	}
	byName := map[string]trips.Member{}
	for _, m := range members {
		byName[m.DisplayName] = m
	}

	// ------------------------------------------------- 4. create events --

	loc := trip.Location()
	departure, err := app.Events.Create(ctx, owner, events.CreateInput{
		Title:   "Departure",
		Type:    events.TypeDeparture,
		StartAt: time.Date(2026, time.September, 23, 19, 0, 0, 0, loc),
	})
	if err != nil {
		t.Fatalf("create departure: %v", err)
	}
	if len(departure.Participants) != 3 {
		t.Errorf("a new event should include the whole group, got %d", len(departure.Participants))
	}
	if departure.Undecided != 3 {
		t.Errorf("nobody has answered yet, but Undecided = %d", departure.Undecided)
	}

	if _, err := app.Events.Create(ctx, owner, events.CreateInput{
		Title:   "Brevet Start",
		Type:    events.TypeRace,
		StartAt: time.Date(2026, time.September, 24, 6, 0, 0, 0, loc),
	}); err != nil {
		t.Fatalf("create brevet start: %v", err)
	}

	// -------------------------------------------- 5. track attendance --

	vasyaAccess := mustAccess(t, app, trip.ID, vasya.ID)
	updated, err := app.Events.SetRSVP(ctx, vasyaAccess, departure.ID, core.Nil, events.RSVPAttending)
	if err != nil {
		t.Fatalf("rsvp: %v", err)
	}
	if updated.Attending != 1 || updated.Undecided != 2 {
		t.Errorf("after one yes: attending=%d undecided=%d", updated.Attending, updated.Undecided)
	}
	// A member may not answer on somebody else's behalf.
	if _, err := app.Events.SetRSVP(ctx, vasyaAccess, departure.ID, byName["Peter"].ID,
		events.RSVPNotAttending); err == nil {
		t.Error("a plain member answered for another participant")
	} else if core.CodeOf(err) != core.CodeForbidden {
		t.Errorf("code = %s, want forbidden", core.CodeOf(err))
	}
	// The organiser may, which is how "I phoned Sasha" gets recorded.
	if _, err := app.Events.SetRSVP(ctx, owner, departure.ID, byName["Peter"].ID,
		events.RSVPAttending); err != nil {
		t.Errorf("organiser could not answer for a member: %v", err)
	}

	// ------------------------------- 6. create, vote on, resolve a decision --

	deadline := time.Now().Add(6 * time.Hour)
	decision, err := app.Decisions.Create(ctx, owner, decisions.CreateInput{
		Title:    "Where should we stay?",
		Options:  []string{"Hotel A", "Apartment B", "Hotel C"},
		Deadline: &deadline,
	})
	if err != nil {
		t.Fatalf("create decision: %v", err)
	}
	if len(decision.Options) != 3 || decision.Eligible != 3 {
		t.Fatalf("decision has %d options for %d voters", len(decision.Options), decision.Eligible)
	}

	apartmentB := decision.Options[1]
	for _, voter := range []core.ID{av.ID, vasya.ID} {
		access := mustAccess(t, app, trip.ID, voter)
		if _, err := app.Decisions.Vote(ctx, access, decision.ID, apartmentB.ID); err != nil {
			t.Fatalf("vote: %v", err)
		}
	}
	// One vote per participant: voting again replaces, never adds.
	if _, err := app.Decisions.Vote(ctx, vasyaAccess, decision.ID, decision.Options[0].ID); err != nil {
		t.Fatalf("changing a vote while open should be allowed: %v", err)
	}
	tally, err := app.Decisions.Get(ctx, owner, decision.ID)
	if err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if tally.TotalVotes != 2 {
		t.Errorf("total votes = %d, want 2", tally.TotalVotes)
	}
	if len(tally.Pending) != 1 || tally.Pending[0].DisplayName != "Peter" {
		t.Errorf("pending voters = %+v, want just Peter", tally.Pending)
	}

	if _, err := app.Decisions.Close(ctx, owner, decision.ID); err != nil {
		t.Fatalf("close decision: %v", err)
	}
	if _, err := app.Decisions.Vote(ctx, vasyaAccess, decision.ID, apartmentB.ID); err == nil {
		t.Error("voting on a closed decision should be refused")
	}
	resolved, err := app.Decisions.Resolve(ctx, owner, decision.ID,
		decisions.ResolveInput{OptionID: apartmentB.ID, Note: "Cheaper and closer to the start"})
	if err != nil {
		t.Fatalf("resolve decision: %v", err)
	}
	chosen, ok := resolved.ResolvedOption()
	if !ok || chosen.Label != "Apartment B" {
		t.Errorf("resolved option = %+v", chosen)
	}

	// ---------------------------------------- 7. organise cars and seats --

	car, err := app.Logistics.Create(ctx, owner, logistics.VehicleInput{
		Name:           "Car 1",
		Type:           logistics.TypeCar,
		Capacity:       4,
		DriverMemberID: byName["Vasya"].ID,
		PassengerIDs:   []core.ID{byName["AV"].ID, byName["Peter"].ID},
	})
	if err != nil {
		t.Fatalf("create vehicle: %v", err)
	}
	// The driver takes a seat: three people in a four-seater leaves one.
	if car.SeatsUsed != 3 || car.SeatsLeft != 1 {
		t.Errorf("seats used=%d left=%d, want 3 and 1", car.SeatsUsed, car.SeatsLeft)
	}
	if _, err := app.Logistics.Update(ctx, owner, car.ID, logistics.VehiclePatch{
		Capacity: intPtr(2),
	}); err == nil {
		t.Error("shrinking a car below its occupants should be refused")
	}

	// ------------------------------------------ 8. organise accommodation --

	place, err := app.Accommodation.Create(ctx, owner, accommodation.Input{
		Name:     "Apartment B",
		Address:  "ul. Piotrkowska 1, Łódź",
		URL:      "https://example.com/apartment-b",
		Capacity: 3,
		GuestIDs: []core.ID{byName["AV"].ID, byName["Vasya"].ID, byName["Peter"].ID},
	})
	if err != nil {
		t.Fatalf("create accommodation: %v", err)
	}
	if place.Occupied != 3 || place.Confirmed != 0 {
		t.Errorf("new guests should be pending: occupied=%d confirmed=%d", place.Occupied, place.Confirmed)
	}
	confirmed, err := app.Accommodation.SetGuestStatus(ctx, vasyaAccess, place.ID, core.Nil,
		accommodation.GuestConfirmed)
	if err != nil {
		t.Fatalf("confirm bed: %v", err)
	}
	if confirmed.Confirmed != 1 {
		t.Errorf("confirmed = %d, want 1", confirmed.Confirmed)
	}

	// ----------------------------------------- 9. shared checklist --

	list, err := app.Checklists.CreateList(ctx, owner, checklists.ListInput{
		Title: "🚴 Bike",
		Scope: checklists.ScopeShared,
		Items: []string{"Pump", "Spare tubes", "Front light", "Rear light"},
	})
	if err != nil {
		t.Fatalf("create checklist: %v", err)
	}
	if list.Progress.Total != 4 || list.Progress.Completed != 0 {
		t.Fatalf("progress = %s", list.Progress)
	}
	done := true
	for _, item := range list.Items[:2] {
		if _, err := app.Checklists.UpdateItem(ctx, vasyaAccess, item.ID,
			checklists.ItemPatch{Completed: &done}); err != nil {
			t.Fatalf("tick item: %v", err)
		}
	}
	progress, err := app.Checklists.Progress(ctx, owner)
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if progress.Completed != 2 || progress.Total != 4 {
		t.Errorf("checklist progress = %s, want 2 / 4 completed", progress)
	}

	// ------------------------------------- 10. expenses and 11. balances --

	// Vasya fuels the car for everyone: €120 split three ways.
	if _, err := app.Expenses.Create(ctx, vasyaAccess, expenses.Input{
		Title:    "Fuel",
		Amount:   12000,
		Category: expenses.CategoryFuel,
		PaidBy:   byName["Vasya"].ID,
	}); err != nil {
		t.Fatalf("create fuel expense: %v", err)
	}
	// AV pays for the apartment: €150, also three ways.
	if _, err := app.Expenses.Create(ctx, owner, expenses.Input{
		Title:    "Apartment B",
		Amount:   15000,
		Category: expenses.CategoryAccommodation,
		PaidBy:   byName["AV"].ID,
	}); err != nil {
		t.Fatalf("create accommodation expense: %v", err)
	}

	report, err := app.Expenses.Balances(ctx, owner)
	if err != nil {
		t.Fatalf("balances: %v", err)
	}
	if report.Total != 27000 {
		t.Errorf("trip total = %s, want 270.00", report.Total)
	}
	balances := map[string]core.Money{}
	for _, b := range report.Balances {
		balances[b.DisplayName] = b.Amount
	}
	// Each owes 90.00. Vasya paid 120 (+30), AV paid 150 (+60), Peter paid
	// nothing (-90).
	for name, want := range map[string]core.Money{"Vasya": 3000, "AV": 6000, "Peter": -9000} {
		if balances[name] != want {
			t.Errorf("%s balance = %s, want %s", name, balances[name], want)
		}
	}
	// One debtor and two creditors clears in two transfers, not three.
	if len(report.Transfers) != 2 {
		t.Fatalf("suggested %d transfers: %+v", len(report.Transfers), report.Transfers)
	}
	for _, transfer := range report.Transfers {
		if transfer.FromName != "Peter" {
			t.Errorf("unexpected payer %q; only Peter owes money", transfer.FromName)
		}
	}

	// ------------------------------------------- 12. mark debts settled --

	peterAccess := mustAccess(t, app, trip.ID, peter.ID)
	for _, transfer := range report.Transfers {
		if _, err := app.Expenses.RecordSettlement(ctx, peterAccess, expenses.SettlementInput{
			From: transfer.From, To: transfer.To, Amount: transfer.Amount,
		}); err != nil {
			t.Fatalf("record settlement: %v", err)
		}
	}
	after, err := app.Expenses.Balances(ctx, owner)
	if err != nil {
		t.Fatalf("balances after settling: %v", err)
	}
	for _, b := range after.Balances {
		if b.Amount != 0 {
			t.Errorf("%s still at %s after settling everything", b.DisplayName, b.Amount)
		}
	}
	if len(after.Transfers) != 0 {
		t.Errorf("nothing should be left to pay, got %+v", after.Transfers)
	}

	// ------------------------------------------------- 13. the dashboard --

	view, err := app.Dashboard.Build(ctx, owner)
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if view.People.Active != 3 {
		t.Errorf("dashboard shows %d people, want 3", view.People.Active)
	}
	if view.Transport.Vehicles != 1 {
		t.Errorf("dashboard shows %d vehicles", view.Transport.Vehicles)
	}
	if view.Accommodation.Places != 1 || view.Accommodation.Confirmed != 1 {
		t.Errorf("accommodation card = %+v", view.Accommodation)
	}
	if view.Expenses.Total != 27000 {
		t.Errorf("dashboard total = %s", view.Expenses.Total)
	}
	if view.Checklist.Completed != 2 || view.Checklist.Total != 4 {
		t.Errorf("dashboard checklist = %s", view.Checklist)
	}
	if view.NextEvent == nil {
		t.Error("the dashboard should name the next event")
	}
	// Two people never confirmed their bed, so the attention engine has
	// something to say.
	if !hasAttention(view.Attention, "accommodation_not_confirmed") {
		t.Errorf("expected an unconfirmed-accommodation warning, got %+v", view.Attention)
	}

	// The activity feed is what gives the group context after the fact.
	feed, err := app.Activity.List(ctx, trip.ID, 100)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(feed) < 8 {
		t.Errorf("activity feed has only %d entries", len(feed))
	}
}

func hasAttention(items []attention.Item, kind string) bool {
	for _, i := range items {
		if string(i.Type) == kind {
			return true
		}
	}
	return false
}

func mustUser(t *testing.T, app *testsupport.App, telegramID int64, name string) users.User {
	t.Helper()
	u, err := app.Users.EnsureUser(t.Context(), users.Identity{
		TelegramID: telegramID, FirstName: name, ChatID: telegramID,
	})
	if err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return u
}

func mustAccess(t *testing.T, app *testsupport.App, tripID, userID core.ID) trips.Access {
	t.Helper()
	access, err := app.Trips.Access(t.Context(), tripID, userID)
	if err != nil {
		t.Fatalf("access: %v", err)
	}
	return access
}

func intPtr(n int) *int { return &n }
