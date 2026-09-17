// Package events is the trip timeline: what happens, when, and who is coming.
package events

import (
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Type classifies an event. It drives the icon in every list, which is most of
// what makes a timeline scannable on a phone.
type Type string

const (
	TypeDeparture     Type = "departure"
	TypeArrival       Type = "arrival"
	TypeAccommodation Type = "accommodation"
	TypeActivity      Type = "activity"
	TypeMeal          Type = "meal"
	TypeTransport     Type = "transport"
	TypeRace          Type = "race"
	TypeCustom        Type = "custom"
)

var allTypes = []Type{
	TypeDeparture, TypeArrival, TypeAccommodation, TypeActivity,
	TypeMeal, TypeTransport, TypeRace, TypeCustom,
}

func (t Type) Valid() bool {
	for _, v := range allTypes {
		if v == t {
			return true
		}
	}
	return false
}

// Icon is the emoji shown next to the event everywhere.
func (t Type) Icon() string {
	switch t {
	case TypeDeparture:
		return "🚗"
	case TypeArrival:
		return "🏁"
	case TypeAccommodation:
		return "🏠"
	case TypeActivity:
		return "🎯"
	case TypeMeal:
		return "🍝"
	case TypeTransport:
		return "🚌"
	case TypeRace:
		return "🚴"
	default:
		return "📌"
	}
}

func TypeStrings() []string {
	out := make([]string, len(allTypes))
	for i, t := range allTypes {
		out[i] = string(t)
	}
	return out
}

// RSVP is a participant's answer for one event.
type RSVP string

const (
	RSVPAttending    RSVP = "attending"
	RSVPNotAttending RSVP = "not_attending"
	RSVPMaybe        RSVP = "maybe"
	RSVPUndecided    RSVP = "undecided"
)

func (s RSVP) Valid() bool {
	switch s {
	case RSVPAttending, RSVPNotAttending, RSVPMaybe, RSVPUndecided:
		return true
	}
	return false
}

// Mark is the checkbox rendered in text views: ☑ / ☐ / ? / ·
func (s RSVP) Mark() string {
	switch s {
	case RSVPAttending:
		return "☑"
	case RSVPNotAttending:
		return "☐"
	case RSVPMaybe:
		return "?"
	default:
		return "·"
	}
}

func RSVPStrings() []string {
	return []string{string(RSVPAttending), string(RSVPNotAttending), string(RSVPMaybe), string(RSVPUndecided)}
}

// Participant is one member's RSVP, denormalised with their name for display.
type Participant struct {
	MemberID    core.ID    `json:"member_id"`
	DisplayName string     `json:"display_name"`
	Status      RSVP       `json:"status"`
	RespondedAt *time.Time `json:"responded_at,omitempty"`
}

// participantRow is the stored form of a Participant. The display name lives
// on trip_members and is joined in, never duplicated here.
type participantRow struct {
	EventID     core.ID `gorm:"primaryKey"`
	MemberID    core.ID `gorm:"primaryKey"`
	Status      RSVP    `gorm:"size:16;not null;default:'undecided'"`
	RespondedAt *time.Time
	CreatedAt   time.Time `gorm:"not null"`
}

func (participantRow) TableName() string { return "event_participants" }

// Event is one entry on the timeline.
type Event struct {
	ID           core.ID    `json:"id"      gorm:"primaryKey"`
	TripID       core.ID    `json:"trip_id" gorm:"not null"`
	Title        string     `json:"title"       gorm:"size:120;not null"`
	Description  string     `json:"description" gorm:"size:2000;not null;default:''"`
	StartAt      time.Time  `json:"start_at"    gorm:"not null"`
	EndAt        *time.Time `json:"end_at,omitempty"`
	LocationName string     `json:"location_name,omitempty" gorm:"size:200;not null;default:''"`
	Latitude     *float64   `json:"latitude,omitempty"`
	Longitude    *float64   `json:"longitude,omitempty"`
	Type         Type       `json:"type"       gorm:"size:20;not null;default:'custom'"`
	CreatedBy    core.ID    `json:"created_by" gorm:"not null"`
	CreatedAt    time.Time  `json:"created_at" gorm:"not null"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"not null"`

	Participants []Participant `json:"participants" gorm:"-"`
	// Counts are what the dashboard and list views need without walking
	// Participants on the client.
	Attending int `json:"attending" gorm:"-"`
	Undecided int `json:"undecided" gorm:"-"`
}

func (Event) TableName() string { return "events" }

// Icon is a convenience for text renderers.
func (e Event) Icon() string { return e.Type.Icon() }

// RSVPOf returns a member's answer, defaulting to undecided.
func (e Event) RSVPOf(memberID core.ID) RSVP {
	for _, p := range e.Participants {
		if p.MemberID == memberID {
			return p.Status
		}
	}
	return RSVPUndecided
}

// Models lists the tables this module owns, for the migration runner. The
// join table stays unexported: nothing outside the repository should build
// one, and reflection does not care about the package boundary.
func Models() []any { return []any{&Event{}, &participantRow{}} }
