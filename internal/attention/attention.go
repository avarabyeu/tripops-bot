// Package attention is the rules engine behind the dashboard's "⚠️ Attention"
// block.
//
// Every rule is deterministic and cheap. There is no scoring model and no AI:
// the product promises that a user can understand the state of the trip in ten
// seconds, and that only works if the reasons are things a person would say out
// loud ("Sasha has not confirmed accommodation").
package attention

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// Severity orders the list. Critical items are things that will break the trip
// if ignored; warnings need somebody to act; info is a nudge.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	default:
		return 1
	}
}

// Type identifies the rule that produced an item, so the client can pick an
// icon and a destination without parsing the text.
type Type string

const (
	TypeEventUndecided        Type = "event_undecided"
	TypeDecisionVotePending   Type = "decision_vote_pending"
	TypeDecisionClosingSoon   Type = "decision_closing_soon"
	TypeDecisionNeedsOutcome  Type = "decision_needs_outcome"
	TypeChecklistAssigned     Type = "checklist_assigned_incomplete"
	TypeAccommodationPending  Type = "accommodation_not_confirmed"
	TypeAccommodationOverbook Type = "accommodation_over_capacity"
	TypeVehicleOverbooked     Type = "vehicle_over_capacity"
	TypeNoSeat                Type = "member_without_seat"
	TypeNoDeparture           Type = "trip_without_departure"
	TypeNoAccommodation       Type = "trip_without_accommodation"
)

// Target points the UI at the thing that needs attention.
type Target struct {
	Kind string  `json:"kind"`
	ID   core.ID `json:"id,omitempty"`
}

// Item is one thing to deal with.
type Item struct {
	Type        Type     `json:"type"`
	Severity    Severity `json:"severity"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Target      Target   `json:"target"`
	// Mine marks items the calling member has to act on personally, which the
	// client renders first.
	Mine bool `json:"mine"`
}

// Sources are the read paths the rules need. They are the module repositories
// rather than their services because every rule is a read and none of them
// should be able to change anything.
type Sources struct {
	Trips         *trips.Service
	Events        *events.Repo
	Decisions     *decisions.Repo
	Vehicles      *logistics.Repo
	Accommodation *accommodation.Repo
	Checklists    *checklists.Repo
}

type Engine struct {
	src Sources
	// now is injectable so the rules are testable without waiting for a clock.
	now func() time.Time
}

func NewEngine(src Sources) *Engine {
	return &Engine{src: src, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock, for tests.
func (e *Engine) WithClock(now func() time.Time) *Engine {
	e.now = now
	return e
}

// Evaluate runs every rule for one trip as seen by one member.
//
// Each rule is independent and failure-tolerant: a rule whose query fails is
// skipped rather than taking the whole dashboard down with it, because a
// partial attention list is far more useful than an error page.
func (e *Engine) Evaluate(ctx context.Context, access trips.Access) []Item {
	now := e.now()
	items := []Item{}
	add := func(i Item) { items = append(items, i) }

	e.eventRules(ctx, access, now, add)
	e.decisionRules(ctx, access, now, add)
	e.logisticsRules(ctx, access, add)
	e.accommodationRules(ctx, access, add)
	e.checklistRules(ctx, access, add)

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Mine != items[j].Mine {
			return items[i].Mine
		}
		if items[i].Severity.rank() != items[j].Severity.rank() {
			return items[i].Severity.rank() > items[j].Severity.rank()
		}
		return items[i].Title < items[j].Title
	})
	return items
}

// eventRules covers undecided RSVPs and a trip with no way of leaving.
func (e *Engine) eventRules(ctx context.Context, access trips.Access, now time.Time, add func(Item)) {
	upcoming, err := e.src.Events.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	rosters, err := e.src.Events.ParticipantsByEvent(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	for _, ev := range upcoming {
		if ev.StartAt.Before(now) {
			continue
		}
		var undecided []string
		mine := false
		for _, p := range rosters[ev.ID] {
			if p.Status != events.RSVPUndecided {
				continue
			}
			undecided = append(undecided, p.DisplayName)
			if p.MemberID == access.Member.ID {
				mine = true
			}
		}
		if len(undecided) == 0 {
			continue
		}
		severity := SeverityInfo
		if ev.StartAt.Sub(now) < 48*time.Hour {
			severity = SeverityWarning
		}
		title := fmt.Sprintf("%d people have not answered about %q", len(undecided), ev.Title)
		if mine {
			title = fmt.Sprintf("You have not answered about %q", ev.Title)
		} else if len(undecided) == 1 {
			title = fmt.Sprintf("%s has not answered about %q", undecided[0], ev.Title)
		}
		add(Item{
			Type: TypeEventUndecided, Severity: severity, Mine: mine,
			Title:       title,
			Description: ev.StartAt.In(access.Trip.Location()).Format("2 Jan at 15:04"),
			Target:      Target{Kind: "event", ID: ev.ID},
		})
	}

	// A trip nobody has scheduled a departure for is the most common gap in a
	// half-planned trip.
	if hasDeparture, err := e.src.Events.HasType(ctx, access.Trip.ID, events.TypeDeparture); err == nil && !hasDeparture {
		add(Item{
			Type: TypeNoDeparture, Severity: SeverityWarning,
			Title:       "No departure planned",
			Description: "Add a departure event so everyone knows when you leave.",
			Target:      Target{Kind: "timeline"},
		})
	}
}

func (e *Engine) decisionRules(ctx context.Context, access trips.Access, now time.Time, add func(Item)) {
	list, err := e.src.Decisions.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	votes, err := e.src.Decisions.Votes(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	votedBy := map[core.ID]map[core.ID]bool{}
	for _, v := range votes {
		if votedBy[v.DecisionID] == nil {
			votedBy[v.DecisionID] = map[core.ID]bool{}
		}
		votedBy[v.DecisionID][v.MemberID] = true
	}

	for _, d := range list {
		switch d.Status {
		case decisions.StatusOpen:
			if !votedBy[d.ID][access.Member.ID] {
				severity := SeverityWarning
				description := "Your vote is missing."
				if d.Deadline != nil {
					left := d.Deadline.Sub(now)
					description = fmt.Sprintf("Voting closes %s.", humanDuration(left))
					if left < 24*time.Hour {
						severity = SeverityCritical
					}
				}
				add(Item{
					Type: TypeDecisionVotePending, Severity: severity, Mine: true,
					Title: "Vote on " + d.Title, Description: description,
					Target: Target{Kind: "decision", ID: d.ID},
				})
				continue
			}
			if d.Deadline != nil && d.Deadline.Sub(now) < 24*time.Hour {
				add(Item{
					Type: TypeDecisionClosingSoon, Severity: SeverityInfo,
					Title:       d.Title + " closes soon",
					Description: fmt.Sprintf("Voting closes %s.", humanDuration(d.Deadline.Sub(now))),
					Target:      Target{Kind: "decision", ID: d.ID},
				})
			}
		case decisions.StatusClosed:
			// Voting is over but nobody has made the call, which is the state
			// where a group silently loses a decision.
			if access.IsManager() {
				add(Item{
					Type: TypeDecisionNeedsOutcome, Severity: SeverityWarning, Mine: true,
					Title:       "Confirm the outcome of " + d.Title,
					Description: "Voting is closed. Pick the final option so everyone sees it.",
					Target:      Target{Kind: "decision", ID: d.ID},
				})
			}
		}
	}
}

func (e *Engine) logisticsRules(ctx context.Context, access trips.Access, add func(Item)) {
	vehicles, err := e.src.Vehicles.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	seating, err := e.src.Vehicles.PassengersByVehicle(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	for _, v := range vehicles {
		ids := make([]core.ID, 0, len(seating[v.ID]))
		for _, p := range seating[v.ID] {
			ids = append(ids, p.MemberID)
		}
		taken := logistics.SeatsTaken(v.DriverMemberID, ids)
		if taken > v.Capacity {
			add(Item{
				Type: TypeVehicleOverbooked, Severity: SeverityCritical,
				Title:       v.Name + " is over capacity",
				Description: fmt.Sprintf("%d people for %d seats, driver included.", taken, v.Capacity),
				Target:      Target{Kind: "vehicle", ID: v.ID},
			})
		}
	}
	if len(vehicles) == 0 {
		return
	}
	unseated, err := e.src.Vehicles.UnseatedMembers(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	for _, id := range unseated {
		if id == access.Member.ID {
			add(Item{
				Type: TypeNoSeat, Severity: SeverityWarning, Mine: true,
				Title:       "You have no seat yet",
				Description: "Pick a vehicle so the drivers know who they are taking.",
				Target:      Target{Kind: "logistics"},
			})
		}
	}
}

func (e *Engine) accommodationRules(ctx context.Context, access trips.Access, add func(Item)) {
	places, err := e.src.Accommodation.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	// An overnight trip with nowhere to sleep is worth saying out loud.
	if len(places) == 0 {
		if nights := access.Trip.Nights(); nights > 0 {
			add(Item{
				Type: TypeNoAccommodation, Severity: SeverityWarning,
				Title:       fmt.Sprintf("No accommodation for %d night(s)", nights),
				Description: "The trip spans an overnight stay but no place is recorded.",
				Target:      Target{Kind: "accommodation"},
			})
		}
		return
	}

	guests, err := e.src.Accommodation.GuestsByAccommodation(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	for _, place := range places {
		if !accommodation.FitsCapacity(place.Capacity, guests[place.ID]) {
			add(Item{
				Type: TypeAccommodationOverbook, Severity: SeverityCritical,
				Title: place.Name + " is over capacity",
				Description: fmt.Sprintf("%d guests for %d beds.",
					accommodation.BedsTaken(guests[place.ID]), place.Capacity),
				Target: Target{Kind: "accommodation", ID: place.ID},
			})
		}
	}

	pending, err := e.src.Accommodation.PendingGuests(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	for _, p := range pending {
		mine := p.MemberID == access.Member.ID
		title := p.DisplayName + " has not confirmed accommodation"
		if mine {
			title = "You have not confirmed accommodation"
		}
		add(Item{
			Type: TypeAccommodationPending, Severity: SeverityWarning, Mine: mine,
			Title: title, Description: p.AccommodationName,
			Target: Target{Kind: "accommodation", ID: p.AccommodationID},
		})
	}
}

func (e *Engine) checklistRules(ctx context.Context, access trips.Access, add func(Item)) {
	assignments, err := e.src.Checklists.OpenAssignments(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	mine := 0
	for _, a := range assignments {
		if a.MemberID == access.Member.ID {
			mine++
		}
	}
	if mine > 0 {
		add(Item{
			Type: TypeChecklistAssigned, Severity: SeverityWarning, Mine: true,
			Title:       fmt.Sprintf("%d assigned item(s) still open", mine),
			Description: "Items on the checklists are waiting for you.",
			Target:      Target{Kind: "checklist"},
		})
	}
}

// humanDuration renders a rough, readable distance in time.
func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("in %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("in %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("in %d days", int(d.Hours()/24))
	}
}
