package accommodation

import (
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func guests(statuses ...GuestStatus) []Guest {
	out := make([]Guest, len(statuses))
	for i, s := range statuses {
		out[i] = Guest{MemberID: core.NewID(), DisplayName: "guest", Status: s}
	}
	return out
}

func TestBedsTakenIgnoresDeclines(t *testing.T) {
	list := guests(GuestConfirmed, GuestPending, GuestDeclined, GuestConfirmed)
	if got := BedsTaken(list); got != 3 {
		t.Errorf("BedsTaken = %d, want 3 (declines free their bed)", got)
	}
	if got := BedsTaken(nil); got != 0 {
		t.Errorf("BedsTaken(nil) = %d", got)
	}
}

func TestFitsCapacity(t *testing.T) {
	list := guests(GuestConfirmed, GuestConfirmed, GuestPending)
	if !FitsCapacity(3, list) {
		t.Error("three guests fit in three beds")
	}
	if FitsCapacity(2, list) {
		t.Error("three guests must not fit in two beds")
	}
	// Capacity zero means the group did not bother counting beds.
	if !FitsCapacity(0, list) {
		t.Error("capacity 0 means unlimited")
	}
	// A declined guest frees the bed they were holding.
	if !FitsCapacity(2, guests(GuestConfirmed, GuestConfirmed, GuestDeclined)) {
		t.Error("a decline should free a bed")
	}
}

func TestRecomputeCounts(t *testing.T) {
	place := Accommodation{
		Capacity: 6,
		Guests:   guests(GuestConfirmed, GuestConfirmed, GuestPending, GuestDeclined),
	}
	place.recompute()
	if place.Confirmed != 2 {
		t.Errorf("Confirmed = %d, want 2", place.Confirmed)
	}
	if place.Pending != 1 {
		t.Errorf("Pending = %d, want 1", place.Pending)
	}
	if place.Occupied != 3 {
		t.Errorf("Occupied = %d, want 3", place.Occupied)
	}
	if place.Overbooked() {
		t.Error("three of six beds is not overbooked")
	}

	place.Capacity = 2
	place.recompute()
	if !place.Overbooked() {
		t.Error("three guests in two beds should be overbooked")
	}
}

func TestGuestStatusValidation(t *testing.T) {
	for _, s := range []GuestStatus{GuestPending, GuestConfirmed, GuestDeclined} {
		if !s.Valid() || s.Mark() == "" {
			t.Errorf("%s should be a valid status with a mark", s)
		}
	}
	if GuestStatus("maybe").Valid() {
		t.Error("unknown guest statuses must not validate")
	}
}
