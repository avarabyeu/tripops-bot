// Package accommodation answers "where are we sleeping": places, dates and who
// is staying in each.
//
// There is no booking-provider integration and there will not be one in the
// MVP. A place has a link field; the link goes wherever the group actually
// booked.
package accommodation

import (
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// GuestStatus tracks whether a member has confirmed their bed. The dashboard's
// "6 / 6 confirmed" comes straight from this.
type GuestStatus string

const (
	GuestPending   GuestStatus = "pending"
	GuestConfirmed GuestStatus = "confirmed"
	GuestDeclined  GuestStatus = "declined"
)

func (s GuestStatus) Valid() bool {
	switch s {
	case GuestPending, GuestConfirmed, GuestDeclined:
		return true
	}
	return false
}

func (s GuestStatus) Mark() string {
	switch s {
	case GuestConfirmed:
		return "☑"
	case GuestDeclined:
		return "☐"
	default:
		return "·"
	}
}

func GuestStatusStrings() []string {
	return []string{string(GuestPending), string(GuestConfirmed), string(GuestDeclined)}
}

// Guest is one member's place in an accommodation.
type Guest struct {
	MemberID    core.ID     `json:"member_id"`
	DisplayName string      `json:"display_name"`
	Status      GuestStatus `json:"status"`
}

// Accommodation is one place to stay.
type Accommodation struct {
	ID       core.ID    `json:"id"      gorm:"primaryKey"`
	TripID   core.ID    `json:"trip_id" gorm:"not null;index"`
	Name     string     `json:"name"              gorm:"size:120;not null"`
	Address  string     `json:"address,omitempty" gorm:"size:300;not null;default:''"`
	URL      string     `json:"url,omitempty"     gorm:"size:512;not null;default:''"`
	CheckIn  *time.Time `json:"check_in,omitempty"`
	CheckOut *time.Time `json:"check_out,omitempty"`
	// Capacity of zero means "not tracked": the group booked a place and does
	// not care to count beds in the app.
	Capacity  int       `json:"capacity" gorm:"not null;default:0"`
	Notes     string    `json:"notes,omitempty" gorm:"size:1000;not null;default:''"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`

	Guests    []Guest `json:"guests"    gorm:"-"`
	Confirmed int     `json:"confirmed" gorm:"-"`
	Pending   int     `json:"pending"   gorm:"-"`
	// Occupied counts everyone who has not declined: those are the beds that
	// are actually spoken for.
	Occupied int `json:"occupied" gorm:"-"`
}

func (Accommodation) TableName() string { return "accommodations" }

// guestRow is the stored guest assignment.
type guestRow struct {
	AccommodationID core.ID     `gorm:"primaryKey"`
	MemberID        core.ID     `gorm:"primaryKey"`
	Status          GuestStatus `gorm:"size:16;not null;default:'pending'"`
	CreatedAt       time.Time   `gorm:"not null"`
}

func (guestRow) TableName() string { return "accommodation_guests" }

// BedsTaken counts guests occupying a bed, i.e. everyone who has not declined.
func BedsTaken(guests []Guest) int {
	n := 0
	for _, g := range guests {
		if g.Status != GuestDeclined {
			n++
		}
	}
	return n
}

// FitsCapacity reports whether the guests fit. Capacity 0 means unlimited.
func FitsCapacity(capacity int, guests []Guest) bool {
	return capacity == 0 || BedsTaken(guests) <= capacity
}

// Overbooked reports whether more beds are claimed than exist.
func (a Accommodation) Overbooked() bool { return a.Capacity > 0 && a.Occupied > a.Capacity }

func (a *Accommodation) recompute() {
	a.Confirmed, a.Pending, a.Occupied = 0, 0, 0
	for _, g := range a.Guests {
		switch g.Status {
		case GuestConfirmed:
			a.Confirmed++
		case GuestPending:
			a.Pending++
		}
	}
	a.Occupied = BedsTaken(a.Guests)
	if a.Guests == nil {
		a.Guests = []Guest{}
	}
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Accommodation{}, &guestRow{}} }
