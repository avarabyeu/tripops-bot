// Package logistics answers "how are we getting there": vehicles, who drives,
// and who sits where.
package logistics

import (
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Type of vehicle. Trains and buses are here too: the group still needs to
// know who is on which one.
type Type string

const (
	TypeCar   Type = "car"
	TypeVan   Type = "van"
	TypeTrain Type = "train"
	TypeBus   Type = "bus"
	TypeOther Type = "other"
)

var allTypes = []Type{TypeCar, TypeVan, TypeTrain, TypeBus, TypeOther}

func (t Type) Valid() bool {
	for _, v := range allTypes {
		if v == t {
			return true
		}
	}
	return false
}

func (t Type) Icon() string {
	switch t {
	case TypeVan:
		return "🚐"
	case TypeTrain:
		return "🚆"
	case TypeBus:
		return "🚌"
	case TypeOther:
		return "🧭"
	default:
		return "🚗"
	}
}

func TypeStrings() []string {
	out := make([]string, len(allTypes))
	for i, t := range allTypes {
		out[i] = string(t)
	}
	return out
}

// Passenger is a member riding in a vehicle.
type Passenger struct {
	MemberID    core.ID `json:"member_id"`
	DisplayName string  `json:"display_name"`
	SeatNote    string  `json:"seat_note,omitempty"`
}

// Vehicle is one car, van or booked train carriage.
type Vehicle struct {
	ID        core.ID   `json:"id"      gorm:"primaryKey"`
	TripID    core.ID   `json:"trip_id" gorm:"not null;index"`
	Name      string    `json:"name"     gorm:"size:80;not null"`
	Type      Type      `json:"type"     gorm:"size:16;not null;default:'car'"`
	Capacity  int       `json:"capacity" gorm:"not null"`
	Notes     string    `json:"notes,omitempty" gorm:"size:500;not null;default:''"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`

	DriverMemberID core.ID `json:"driver_member_id,omitempty"`
	DriverName     string  `json:"driver_name,omitempty" gorm:"-"`

	Passengers []Passenger `json:"passengers" gorm:"-"`
	// SeatsUsed and SeatsLeft are derived; they are serialised so the client
	// never has to reimplement the rule that the driver occupies a seat.
	SeatsUsed int `json:"seats_used" gorm:"-"`
	SeatsLeft int `json:"seats_left" gorm:"-"`
}

func (Vehicle) TableName() string { return "vehicles" }

// passengerRow is the stored seat assignment. The passenger's name lives on
// trip_members and is joined in, never duplicated here.
type passengerRow struct {
	VehicleID core.ID   `gorm:"primaryKey"`
	MemberID  core.ID   `gorm:"primaryKey"`
	SeatNote  string    `gorm:"size:80;not null;default:''"`
	CreatedAt time.Time `gorm:"not null"`
}

func (passengerRow) TableName() string { return "vehicle_passengers" }

// SeatsTaken counts occupied seats. The driver takes a seat, and is counted
// once even if they also appear in the passenger list, which is exactly the
// bug this function exists to prevent.
func SeatsTaken(driver core.ID, passengers []core.ID) int {
	seen := map[core.ID]bool{}
	if !driver.IsZero() {
		seen[driver] = true
	}
	for _, p := range passengers {
		if p.IsZero() {
			continue
		}
		seen[p] = true
	}
	return len(seen)
}

// FitsCapacity reports whether the occupants fit in capacity seats.
func FitsCapacity(capacity int, driver core.ID, passengers []core.ID) bool {
	return SeatsTaken(driver, passengers) <= capacity
}

// recompute fills the derived seat counters.
func (v *Vehicle) recompute() {
	ids := make([]core.ID, 0, len(v.Passengers))
	for _, p := range v.Passengers {
		ids = append(ids, p.MemberID)
	}
	v.SeatsUsed = SeatsTaken(v.DriverMemberID, ids)
	v.SeatsLeft = v.Capacity - v.SeatsUsed
	if v.Passengers == nil {
		v.Passengers = []Passenger{}
	}
}

// Overbooked reports whether more people are assigned than seats exist, which
// can only happen if capacity was lowered after seats were handed out.
func (v Vehicle) Overbooked() bool { return v.SeatsUsed > v.Capacity }

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Vehicle{}, &passengerRow{}} }
