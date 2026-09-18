// Package scheduler turns the passage of time into notifications.
//
// It is a plain ticker that re-evaluates a handful of deterministic rules.
// There is no job queue and no cron expressions because there is nothing to
// schedule: every rule asks "what is true right now" and enqueues messages
// whose dedupe keys make a second evaluation a no-op. That is what lets the
// process restart, run twice, or lag for an hour without spamming anyone.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// Deps are the services the rules read and write.
type Deps struct {
	Trips         *trips.Service
	Decisions     *decisions.Service
	Events        *events.Repo
	Checklists    *checklists.Repo
	Accommodation *accommodation.Repo
	Expenses      *expenses.Repo
	Notify        *notify.Service
}

type Scheduler struct {
	deps Deps
	log  *slog.Logger
	now  func() time.Time
}

func New(deps Deps, log *slog.Logger) *Scheduler {
	return &Scheduler{deps: deps, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock. Every rule here is a question about time, so
// tests set it rather than waiting for one.
func (s *Scheduler) WithClock(now func() time.Time) *Scheduler {
	s.now = now
	return s
}

// Run ticks until the context is cancelled.
func (s *Scheduler) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.Tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// Tick runs every rule once. Each rule is independent: one failing does not
// stop the others.
func (s *Scheduler) Tick(ctx context.Context) {
	now := s.now()

	if closed, err := s.deps.Decisions.AutoCloseDue(ctx, now); err != nil {
		s.log.Warn("auto-close decisions failed", "err", err)
	} else if closed > 0 {
		s.log.Info("decisions auto-closed", "count", closed)
	}

	if err := s.remindVoters(ctx, now); err != nil {
		s.log.Warn("voting reminders failed", "err", err)
	}

	// Before anything reads a status: a trip that has happened should not
	// still say "planning" on the screen somebody opens next.
	if advanced, err := s.deps.Trips.AdvanceStatuses(ctx, now); err != nil {
		s.log.Warn("advance trip statuses failed", "err", err)
	} else if advanced > 0 {
		s.log.Info("trip statuses advanced", "count", advanced)
	}

	today := core.DateOf(now, time.UTC)
	tripList, err := s.deps.Trips.Upcoming(ctx, today, 30)
	if err != nil {
		s.log.Warn("load upcoming trips failed", "err", err)
		return
	}
	for _, trip := range tripList {
		if err := s.remindEvents(ctx, trip, now); err != nil {
			s.log.Warn("event reminders failed", "trip_id", trip.ID, "err", err)
		}
		if err := s.remindChecklists(ctx, trip, now); err != nil {
			s.log.Warn("checklist reminders failed", "trip_id", trip.ID, "err", err)
		}
		if err := s.remindAccommodation(ctx, trip, now); err != nil {
			s.log.Warn("accommodation reminders failed", "trip_id", trip.ID, "err", err)
		}
	}

	// Settling up is the one thing that happens after a trip, so it walks its
	// own list: UpcomingTrips stops at the end date by design.
	ended, err := s.deps.Trips.RecentlyEnded(ctx, today, settleUpWindowDays)
	if err != nil {
		s.log.Warn("load recently ended trips failed", "err", err)
		return
	}
	for _, trip := range ended {
		if err := s.remindSettleUp(ctx, trip, now); err != nil {
			s.log.Warn("settle-up reminders failed", "trip_id", trip.ID, "err", err)
		}
	}
}

// settleUpWindowDays is how far back the settle-up rule looks. It exists so a
// process that was down over the weekend still sends the nudge; the dedupe key
// is what keeps it to exactly one per person per trip.
const settleUpWindowDays = 7

// settleUpHour is the local hour the nudge goes out. "You owe Anna 40.00" at
// midnight is a worse message than the same one over breakfast, and unlike the
// pre-trip reminders this one is never urgent.
const settleUpHour = 9

// remindSettleUp tells everyone who is not square that the trip is over.
//
// Once per person per trip, ever. If the group ignores it the product has said
// its piece: four to eight friends do not need a collections department, and a
// daily debt chaser is the fastest way to get a bot muted.
func (s *Scheduler) remindSettleUp(ctx context.Context, trip trips.Trip, now time.Time) error {
	if now.In(trip.Location()).Hour() < settleUpHour {
		return nil
	}
	balances, err := s.deps.Expenses.Balances(ctx, trip.ID)
	if err != nil {
		return err
	}
	transfers, err := expenses.MinimalTransfers(balances, trip.Currency)
	if err != nil {
		// Balances that do not sum to zero mean a bug upstream. Do not guess
		// at what to tell people about their money.
		return err
	}
	if len(transfers) == 0 {
		return nil
	}

	members, err := s.deps.Trips.MembersOf(ctx, trip.ID)
	if err != nil {
		return err
	}
	userOf := make(map[core.ID]core.ID, len(members))
	for _, m := range members {
		if m.Active() {
			userOf[m.ID] = m.UserID
		}
	}

	// The largest transfer each person is part of: one line is enough to make
	// somebody open the app, and the balances screen has the rest.
	type nudge struct {
		body   string
		amount core.Money
	}
	best := map[core.ID]nudge{}
	consider := func(memberID core.ID, amount core.Money, body string) {
		if current, seen := best[memberID]; seen && current.amount >= amount {
			return
		}
		best[memberID] = nudge{body: body, amount: amount}
	}
	for _, t := range transfers {
		consider(t.From, t.Amount, fmt.Sprintf("You owe %s %s.", t.ToName, t.Amount.Format(trip.Currency)))
		consider(t.To, t.Amount, fmt.Sprintf("%s still owes you %s.", t.FromName, t.Amount.Format(trip.Currency)))
	}

	items := make([]notify.Notification, 0, len(best))
	for memberID, n := range best {
		userID, ok := userOf[memberID]
		if !ok {
			continue
		}
		items = append(items, notify.Notification{
			TripID: trip.ID,
			UserID: userID,
			// Reminders, not expenses. The `expenses` category is the
			// firehose — every bill anybody records — and it is off by
			// default for exactly that reason, so a nudge sent under it would
			// reach nobody. This is a one-off, time-triggered reminder, which
			// is what `reminders` already means.
			Category: notify.CategoryReminders,
			Title:    "💶 " + trip.Title,
			Body:     n.body + " The trip is over — settle up when you can.",
			// No date in the key: this is once per trip, not once per day.
			DedupeKey: notify.DedupeKey("settlement_reminder", trip.ID.String(), userID.String()),
		})
	}
	return s.deps.Notify.Enqueue(ctx, items...)
}

// remindVoters nudges people who have not voted on a decision closing within
// two hours. One ping per person per decision, ever.
func (s *Scheduler) remindVoters(ctx context.Context, now time.Time) error {
	closing, err := s.deps.Decisions.Repo().ClosingSoon(ctx, now, now.Add(2*time.Hour))
	if err != nil {
		return err
	}
	for _, d := range closing {
		pending, err := s.deps.Decisions.Repo().NonVoters(ctx, d.ID)
		if err != nil {
			return err
		}
		items := make([]notify.Notification, 0, len(pending))
		for _, userID := range pending {
			items = append(items, notify.Notification{
				TripID:    d.TripID,
				UserID:    userID,
				Category:  notify.CategoryDecisions,
				Title:     "🗳 Voting closes soon",
				Body:      fmt.Sprintf("%s\nVoting closes %s.", d.Title, humanUntil(*d.Deadline, now)),
				DedupeKey: notify.DedupeKey("decision_reminder", d.ID.String(), userID.String()),
				Payload:   map[string]any{"decision_id": d.ID.String()},
			})
		}
		if err := s.deps.Notify.Enqueue(ctx, items...); err != nil {
			return err
		}
	}
	return nil
}

// remindEvents sends the "tomorrow" digest: every event starting in the next
// 24 hours, once per attendee.
func (s *Scheduler) remindEvents(ctx context.Context, trip trips.Trip, now time.Time) error {
	upcoming, err := s.deps.Events.InRange(ctx, trip.ID, now, now.Add(24*time.Hour))
	if err != nil {
		return err
	}
	loc := trip.Location()
	for _, e := range upcoming {
		attendees, err := s.deps.Events.AttendeeUserIDs(ctx, e.ID)
		if err != nil {
			return err
		}
		items := make([]notify.Notification, 0, len(attendees))
		for _, userID := range attendees {
			items = append(items, notify.Notification{
				TripID:   trip.ID,
				UserID:   userID,
				Category: notify.CategoryReminders,
				Title:    "📅 " + trip.Title,
				Body: fmt.Sprintf("%s %s\n%s", e.Icon(), e.Title,
					e.StartAt.In(loc).Format("Monday 2 January, 15:04")),
				DedupeKey: notify.DedupeKey("event_reminder", e.ID.String(), userID.String()),
				Payload:   map[string]any{"event_id": e.ID.String()},
			})
		}
		if err := s.deps.Notify.Enqueue(ctx, items...); err != nil {
			return err
		}
	}
	return nil
}

// remindChecklists nudges people with open assigned items in the two days
// before the trip — early enough to actually buy the thing.
func (s *Scheduler) remindChecklists(ctx context.Context, trip trips.Trip, now time.Time) error {
	days := core.DateOf(now, trip.Location()).DaysUntil(trip.StartDate)
	if days < 0 || days > 2 {
		return nil
	}
	assignments, err := s.deps.Checklists.OpenAssignments(ctx, trip.ID)
	if err != nil {
		return err
	}
	perUser := map[core.ID]int{}
	for _, a := range assignments {
		perUser[a.UserID]++
	}
	today := core.DateOf(now, time.UTC).String()
	items := make([]notify.Notification, 0, len(perUser))
	for userID, count := range perUser {
		items = append(items, notify.Notification{
			TripID:    trip.ID,
			UserID:    userID,
			Category:  notify.CategoryChecklist,
			Title:     "🎒 " + trip.Title,
			Body:      fmt.Sprintf("You still have %d assigned item(s) incomplete.", count),
			DedupeKey: notify.DedupeKey("checklist_reminder", trip.ID.String(), userID.String(), today),
		})
	}
	return s.deps.Notify.Enqueue(ctx, items...)
}

// remindAccommodation chases people who have a bed reserved but never said yes.
func (s *Scheduler) remindAccommodation(ctx context.Context, trip trips.Trip, now time.Time) error {
	days := core.DateOf(now, trip.Location()).DaysUntil(trip.StartDate)
	if days < 0 || days > 3 {
		return nil
	}
	pending, err := s.deps.Accommodation.PendingGuests(ctx, trip.ID)
	if err != nil {
		return err
	}
	today := core.DateOf(now, time.UTC).String()
	items := make([]notify.Notification, 0, len(pending))
	for _, p := range pending {
		items = append(items, notify.Notification{
			TripID:    trip.ID,
			UserID:    p.UserID,
			Category:  notify.CategoryReminders,
			Title:     "🏠 " + trip.Title,
			Body:      fmt.Sprintf("Please confirm your place at %s.", p.AccommodationName),
			DedupeKey: notify.DedupeKey("accommodation_reminder", p.AccommodationID.String(), p.UserID.String(), today),
			Payload:   map[string]any{"accommodation_id": p.AccommodationID.String()},
		})
	}
	return s.deps.Notify.Enqueue(ctx, items...)
}

func humanUntil(deadline, now time.Time) string {
	d := deadline.Sub(now)
	switch {
	case d <= 0:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("in %d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("in %d hours", int(d.Hours())+1)
	}
}
