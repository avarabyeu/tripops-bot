package events

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func TestRSVPMarks(t *testing.T) {
	cases := map[RSVP]string{
		RSVPAttending:    "☑",
		RSVPNotAttending: "☐",
		RSVPMaybe:        "?",
		RSVPUndecided:    "·",
	}
	for status, want := range cases {
		if got := status.Mark(); got != want {
			t.Errorf("%s.Mark() = %q, want %q", status, got, want)
		}
		if !status.Valid() {
			t.Errorf("%s should be valid", status)
		}
	}
	if RSVP("perhaps").Valid() {
		t.Error("unknown RSVP values must not validate")
	}
}

func TestEventTypeIcons(t *testing.T) {
	for _, tp := range []Type{
		TypeDeparture, TypeArrival, TypeAccommodation, TypeActivity,
		TypeMeal, TypeTransport, TypeRace, TypeCustom,
	} {
		if !tp.Valid() {
			t.Errorf("%s should be valid", tp)
		}
		if tp.Icon() == "" {
			t.Errorf("%s needs an icon", tp)
		}
	}
	if Type("teleport").Valid() {
		t.Error("unknown event types must not validate")
	}
}

func TestAttachCountsParticipation(t *testing.T) {
	me := core.NewID()
	event := Event{StartAt: time.Now()}
	attach(&event, []Participant{
		{MemberID: me, DisplayName: "AV", Status: RSVPAttending},
		{MemberID: core.NewID(), DisplayName: "Vasya", Status: RSVPAttending},
		{MemberID: core.NewID(), DisplayName: "Sasha", Status: RSVPUndecided},
		{MemberID: core.NewID(), DisplayName: "Misha", Status: RSVPNotAttending},
		{MemberID: core.NewID(), DisplayName: "Peter", Status: RSVPMaybe},
	})
	if event.Attending != 2 {
		t.Errorf("Attending = %d, want 2", event.Attending)
	}
	if event.Undecided != 1 {
		t.Errorf("Undecided = %d, want 1", event.Undecided)
	}
	if got := event.RSVPOf(me); got != RSVPAttending {
		t.Errorf("RSVPOf(me) = %s", got)
	}
	// Somebody who is not on the roster at all has simply not answered.
	if got := event.RSVPOf(core.NewID()); got != RSVPUndecided {
		t.Errorf("RSVPOf(stranger) = %s, want undecided", got)
	}
}

func TestAttachNormalisesNilRoster(t *testing.T) {
	event := Event{}
	attach(&event, nil)
	if event.Participants == nil {
		t.Error("an empty roster must serialise as [] rather than null")
	}
}
