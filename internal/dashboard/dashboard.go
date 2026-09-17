// Package dashboard composes the trip home screen.
//
// It owns no data. Its whole job is to answer, in one round trip, the nine
// questions the product promises to answer instantly: who is going, when are we
// leaving, what did we decide, how are we getting there, where are we sleeping,
// what do I bring, who paid, who owes whom, and what needs me.
package dashboard

import (
	"context"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/attention"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// People is the "5 / 6 confirmed" block.
type People struct {
	Active  int `json:"active"`
	Invited int `json:"invited"`
	Total   int `json:"total"`
}

// Transport is the vehicle summary.
type Transport struct {
	Vehicles  int `json:"vehicles"`
	Seats     int `json:"seats"`
	SeatsUsed int `json:"seats_used"`
	Unseated  int `json:"unseated"`
}

// Accommodation is the beds summary.
type Accommodation struct {
	Places    int `json:"places"`
	Confirmed int `json:"confirmed"`
	Total     int `json:"total"`
}

// Expenses is the money summary, including the caller's own position.
type Expenses struct {
	Total     core.Money `json:"total_minor"`
	Currency  string     `json:"currency"`
	Count     int        `json:"count"`
	MyBalance core.Money `json:"my_balance_minor"`
}

// Decisions counts what is still open.
type Decisions struct {
	Open           int `json:"open"`
	AwaitingMyVote int `json:"awaiting_my_vote"`
}

// View is the whole dashboard.
type View struct {
	Trip          trips.Trip          `json:"trip"`
	Me            trips.Member        `json:"me"`
	People        People              `json:"people"`
	Transport     Transport           `json:"transport"`
	Accommodation Accommodation       `json:"accommodation"`
	NextEvent     *events.Event       `json:"next_event,omitempty"`
	Expenses      Expenses            `json:"expenses"`
	Decisions     Decisions           `json:"decisions"`
	Checklist     checklists.Progress `json:"checklist"`
	Attention     []attention.Item    `json:"attention"`
	// DaysUntilStart is negative once the trip has begun.
	DaysUntilStart int `json:"days_until_start"`
}

// Sources are the repositories the dashboard reads.
type Sources struct {
	Trips         *trips.Service
	Events        *events.Repo
	Decisions     *decisions.Repo
	Vehicles      *logistics.Repo
	Accommodation *accommodation.Repo
	Checklists    *checklists.Repo
	Expenses      *expenses.Repo
	Attention     *attention.Engine
}

type Service struct {
	src Sources
	now func() time.Time
}

func NewService(src Sources) *Service {
	return &Service{src: src, now: func() time.Time { return time.Now().UTC() }}
}

// Build assembles the dashboard for one member.
//
// Each section degrades on its own: a failure in, say, the vehicle query
// leaves that card empty instead of failing the screen the user opened to find
// out what is going on.
func (s *Service) Build(ctx context.Context, access trips.Access) (View, error) {
	now := s.now()
	view := View{
		Trip:           access.Trip,
		Me:             access.Member,
		Attention:      []attention.Item{},
		DaysUntilStart: core.DateOf(now, access.Trip.Location()).DaysUntil(access.Trip.StartDate),
	}

	if counts, err := s.src.Trips.MemberCounts(ctx, access.Trip.ID); err == nil {
		view.People = People{Active: counts.Active, Invited: counts.Invited, Total: counts.Total}
	}

	if vehicles, err := s.src.Vehicles.ListByTrip(ctx, access.Trip.ID); err == nil {
		seating, _ := s.src.Vehicles.PassengersByVehicle(ctx, access.Trip.ID)
		view.Transport.Vehicles = len(vehicles)
		for _, v := range vehicles {
			ids := make([]core.ID, 0, len(seating[v.ID]))
			for _, p := range seating[v.ID] {
				ids = append(ids, p.MemberID)
			}
			view.Transport.Seats += v.Capacity
			view.Transport.SeatsUsed += logistics.SeatsTaken(v.DriverMemberID, ids)
		}
		if unseated, err := s.src.Vehicles.UnseatedMembers(ctx, access.Trip.ID); err == nil {
			view.Transport.Unseated = len(unseated)
		}
	}

	if counts, err := s.src.Accommodation.Counts(ctx, access.Trip.ID); err == nil {
		view.Accommodation = Accommodation{Places: counts.Places, Confirmed: counts.Confirmed, Total: counts.Total}
	}

	if next, err := s.src.Events.NextAfter(ctx, access.Trip.ID, now); err == nil {
		rosters, _ := s.src.Events.ParticipantsByEvent(ctx, access.Trip.ID)
		next.Participants = rosters[next.ID]
		for _, p := range next.Participants {
			switch p.Status {
			case events.RSVPAttending:
				next.Attending++
			case events.RSVPUndecided:
				next.Undecided++
			}
		}
		view.NextEvent = &next
	}

	view.Expenses.Currency = access.Trip.Currency
	if total, count, err := s.src.Expenses.TripTotal(ctx, access.Trip.ID); err == nil {
		view.Expenses.Total = total
		view.Expenses.Count = count
	}
	if balances, err := s.src.Expenses.Balances(ctx, access.Trip.ID); err == nil {
		for _, b := range balances {
			if b.MemberID == access.Member.ID {
				view.Expenses.MyBalance = b.Amount
			}
		}
	}

	if open, err := s.src.Decisions.CountOpen(ctx, access.Trip.ID); err == nil {
		view.Decisions.Open = open
	}
	if list, err := s.src.Decisions.ListByTrip(ctx, access.Trip.ID); err == nil {
		if votes, err := s.src.Decisions.Votes(ctx, access.Trip.ID); err == nil {
			mine := map[core.ID]bool{}
			for _, v := range votes {
				if v.MemberID == access.Member.ID {
					mine[v.DecisionID] = true
				}
			}
			for _, d := range list {
				if d.Status == decisions.StatusOpen && !mine[d.ID] {
					view.Decisions.AwaitingMyVote++
				}
			}
		}
	}

	if progress, err := s.src.Checklists.TripProgress(ctx, access.Trip.ID, access.Member.ID); err == nil {
		view.Checklist = progress
	}

	view.Attention = s.src.Attention.Evaluate(ctx, access)
	return view, nil
}
