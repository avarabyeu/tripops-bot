package test

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// TestMigrationsAndRoundTrip is the canary for the dual-engine promise: the
// embedded SQL has to create a usable schema, and the value types have to
// survive a write and a read on whichever engine the suite is pointed at.
func TestMigrationsAndRoundTrip(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, err := app.Users.EnsureUser(ctx, users.Identity{
		TelegramID: 1001, FirstName: "Andrei", LastName: "V", Username: "av", ChatID: 555,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if owner.ID.IsZero() {
		t.Fatal("user was created without an id")
	}

	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Brevet Łódź 200",
		StartDate: core.NewDate(2026, time.September, 23),
		EndDate:   core.NewDate(2026, time.September, 24),
		Timezone:  "Europe/Warsaw",
		Currency:  "EUR",
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}

	loaded, err := app.Trips.Get(ctx, trip.ID, owner.ID)
	if err != nil {
		t.Fatalf("load trip: %v", err)
	}
	// Dates are text columns; a round trip that loses a day would break every
	// reminder in the product.
	if loaded.StartDate != core.NewDate(2026, time.September, 23) {
		t.Errorf("start date came back as %v", loaded.StartDate)
	}
	if loaded.EndDate != core.NewDate(2026, time.September, 24) {
		t.Errorf("end date came back as %v", loaded.EndDate)
	}
	if loaded.Timezone != "Europe/Warsaw" {
		t.Errorf("timezone came back as %q", loaded.Timezone)
	}
	if loaded.Nights() != 1 {
		t.Errorf("trip should span one night, got %d", loaded.Nights())
	}
	// Timestamps are written by the application in UTC and must come back
	// within the same second.
	if loaded.CreatedAt.IsZero() {
		t.Error("created_at was not persisted")
	}
	if delta := time.Since(loaded.CreatedAt).Abs(); delta > time.Minute {
		t.Errorf("created_at is %v away from now; a timezone is being lost", delta)
	}

	// Creating a trip must also create the owner membership, in the same
	// transaction: a trip nobody can open is worse than no trip.
	access, err := app.Trips.Access(ctx, trip.ID, owner.ID)
	if err != nil {
		t.Fatalf("owner cannot access their own trip: %v", err)
	}
	if !access.IsOwner() {
		t.Errorf("creator has role %q, want owner", access.Role())
	}
	if access.Member.DisplayName != "Andrei V" {
		t.Errorf("display name = %q", access.Member.DisplayName)
	}
}

// TestUnknownUserCannotSeeTrip proves the authorization root: a non-member is
// told the trip does not exist rather than that they are not allowed in.
func TestUnknownUserCannotSeeTrip(t *testing.T) {
	app := testsupport.NewApp(t)
	ctx := t.Context()

	owner, err := app.Users.EnsureUser(ctx, users.Identity{TelegramID: 2001, FirstName: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	stranger, err := app.Users.EnsureUser(ctx, users.Identity{TelegramID: 2002, FirstName: "Stranger"})
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	trip, err := app.Trips.Create(ctx, owner, trips.CreateInput{
		Title:     "Private trip",
		StartDate: core.NewDate(2026, time.October, 1),
		EndDate:   core.NewDate(2026, time.October, 2),
	})
	if err != nil {
		t.Fatalf("create trip: %v", err)
	}

	_, err = app.Trips.Access(ctx, trip.ID, stranger.ID)
	if err == nil {
		t.Fatal("a stranger got access to somebody else's trip")
	}
	if code := core.CodeOf(err); code != core.CodeNotFound {
		t.Errorf("code = %s, want not_found so membership is not leaked", code)
	}
}
