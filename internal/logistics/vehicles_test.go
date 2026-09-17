package logistics

import (
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func TestSeatsTakenCountsTheDriver(t *testing.T) {
	driver := core.NewID()
	a, b, c := core.NewID(), core.NewID(), core.NewID()

	cases := []struct {
		name       string
		driver     core.ID
		passengers []core.ID
		want       int
	}{
		{name: "driver alone", driver: driver, want: 1},
		{name: "driver and three passengers", driver: driver, passengers: []core.ID{a, b, c}, want: 4},
		{name: "no driver assigned yet", passengers: []core.ID{a, b}, want: 2},
		// The driver is often also in the passenger list in a UI that lets you
		// tick everybody; they must not occupy two seats.
		{name: "driver also listed as passenger", driver: driver, passengers: []core.ID{driver, a}, want: 2},
		{name: "duplicated passenger", driver: driver, passengers: []core.ID{a, a}, want: 2},
		{name: "empty", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SeatsTaken(tc.driver, tc.passengers); got != tc.want {
				t.Errorf("SeatsTaken = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFitsCapacity(t *testing.T) {
	driver := core.NewID()
	a, b, c := core.NewID(), core.NewID(), core.NewID()

	// A four-seat car with a driver takes three more people, not four.
	if !FitsCapacity(4, driver, []core.ID{a, b, c}) {
		t.Error("driver plus three should fit in four seats")
	}
	if FitsCapacity(4, driver, []core.ID{a, b, c, core.NewID()}) {
		t.Error("driver plus four must not fit in four seats")
	}
	if !FitsCapacity(1, driver, nil) {
		t.Error("a driver alone fits in a single seat")
	}
	if FitsCapacity(1, driver, []core.ID{a}) {
		t.Error("two people must not fit in a single seat")
	}
}

func TestVehicleRecomputeAndOverbooked(t *testing.T) {
	driver := core.NewID()
	v := Vehicle{
		Capacity:       3,
		DriverMemberID: driver,
		Passengers: []Passenger{
			{MemberID: driver, DisplayName: "Vasya"},
			{MemberID: core.NewID(), DisplayName: "AV"},
		},
	}
	v.recompute()
	if v.SeatsUsed != 2 {
		t.Errorf("SeatsUsed = %d, want 2", v.SeatsUsed)
	}
	if v.SeatsLeft != 1 {
		t.Errorf("SeatsLeft = %d, want 1", v.SeatsLeft)
	}
	if v.Overbooked() {
		t.Error("two people in three seats is not overbooked")
	}

	// Lowering the capacity after seats were handed out is the only way to
	// reach an overbooked state, and the dashboard must notice.
	v.Capacity = 1
	v.recompute()
	if !v.Overbooked() {
		t.Error("two people in one seat should be overbooked")
	}
	if v.SeatsLeft != -1 {
		t.Errorf("SeatsLeft = %d, want -1", v.SeatsLeft)
	}
}

func TestVehicleTypes(t *testing.T) {
	for _, tp := range []Type{TypeCar, TypeVan, TypeTrain, TypeBus, TypeOther} {
		if !tp.Valid() {
			t.Errorf("%s should be valid", tp)
		}
		if tp.Icon() == "" {
			t.Errorf("%s needs an icon", tp)
		}
	}
	if Type("submarine").Valid() {
		t.Error("unknown vehicle types must not validate")
	}
}
