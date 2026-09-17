package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// Service implements the timeline use cases.
type Service struct {
	database *db.DB
	repo     *Repo
	trips    *trips.Service
	activity activity.Logger
	notify   notify.Enqueuer
}

func NewService(database *db.DB, tripSvc *trips.Service, act activity.Logger, notifier notify.Enqueuer) *Service {
	return &Service{database: database, repo: NewRepo(database.DB), trips: tripSvc, activity: act, notify: notifier}
}

// Repo exposes the repository to sibling read-only services (dashboard,
// attention rules, the reminder scheduler).
func (s *Service) Repo() *Repo { return s.repo }

// CreateInput describes a new timeline entry.
type CreateInput struct {
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	StartAt      time.Time  `json:"start_at"`
	EndAt        *time.Time `json:"end_at"`
	LocationName string     `json:"location_name"`
	Latitude     *float64   `json:"latitude"`
	Longitude    *float64   `json:"longitude"`
	Type         Type       `json:"type"`
	// ParticipantIDs defaults to every active member: a trip event concerns
	// everybody until somebody says otherwise.
	ParticipantIDs []core.ID `json:"participant_ids"`
}

func (in *CreateInput) normalise() {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.LocationName = strings.TrimSpace(in.LocationName)
	if in.Type == "" {
		in.Type = TypeCustom
	}
	in.StartAt = core.UTC(in.StartAt)
	in.EndAt = core.UTCPtr(in.EndAt)
}

func (in CreateInput) validate() error {
	v := core.NewValidator()
	v.Required(in.Title, "title")
	v.Length(in.Title, "title", 1, 120)
	v.Length(in.Description, "description", 0, 2000)
	v.Length(in.LocationName, "location_name", 0, 200)
	v.Check(!in.StartAt.IsZero(), "start_at", "is required")
	v.Check(in.Type.Valid(), "type", "must be one of: "+strings.Join(TypeStrings(), ", "))
	if in.EndAt != nil && !in.StartAt.IsZero() {
		v.Check(!in.EndAt.Before(in.StartAt), "end_at", "must not be before the start")
	}
	if in.Latitude != nil {
		v.Check(*in.Latitude >= -90 && *in.Latitude <= 90, "latitude", "must be between -90 and 90")
	}
	if in.Longitude != nil {
		v.Check(*in.Longitude >= -180 && *in.Longitude <= 180, "longitude", "must be between -180 and 180")
	}
	v.Check((in.Latitude == nil) == (in.Longitude == nil), "longitude", "must be provided together with latitude")
	return v.Err()
}

// Create adds an event and seeds its roster.
func (s *Service) Create(ctx context.Context, access trips.Access, in CreateInput) (Event, error) {
	if err := access.RequireManage(); err != nil {
		return Event{}, err
	}
	in.normalise()
	if err := in.validate(); err != nil {
		return Event{}, err
	}

	roster, err := s.resolveRoster(ctx, access, in.ParticipantIDs)
	if err != nil {
		return Event{}, err
	}

	event := Event{
		ID:           core.NewID(),
		TripID:       access.Trip.ID,
		Title:        in.Title,
		Description:  in.Description,
		StartAt:      in.StartAt,
		EndAt:        in.EndAt,
		LocationName: in.LocationName,
		Latitude:     in.Latitude,
		Longitude:    in.Longitude,
		Type:         in.Type,
		CreatedBy:    access.User.ID,
	}
	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.Insert(ctx, event)
		if err != nil {
			return err
		}
		event = created
		return repo.SetParticipants(ctx, event.ID, roster)
	})
	if err != nil {
		return Event{}, err
	}

	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindEventCreated,
		Message: fmt.Sprintf("%s added %q to the timeline", access.Member.DisplayName, event.Title),
		Meta:    map[string]any{"event_id": event.ID.String()},
	})
	s.fanOut(ctx, access, notify.Notification{
		TripID:   access.Trip.ID,
		Category: notify.CategoryTripUpdates,
		Title:    "📅 " + access.Trip.Title,
		Body: fmt.Sprintf("%s %s — %s", event.Icon(), event.Title,
			event.StartAt.In(access.Trip.Location()).Format("2 Jan, 15:04")),
		Payload: map[string]any{"event_id": event.ID.String()},
	})
	return s.withParticipants(ctx, access.Trip.ID, event)
}

// UpdateInput patches an event; nil fields are untouched.
type UpdateInput struct {
	Title          *string    `json:"title"`
	Description    *string    `json:"description"`
	StartAt        *time.Time `json:"start_at"`
	EndAt          *time.Time `json:"end_at"`
	ClearEndAt     bool       `json:"clear_end_at"`
	LocationName   *string    `json:"location_name"`
	Latitude       *float64   `json:"latitude"`
	Longitude      *float64   `json:"longitude"`
	Type           *Type      `json:"type"`
	ParticipantIDs []core.ID  `json:"participant_ids"`
}

func (s *Service) Update(ctx context.Context, access trips.Access, eventID core.ID, in UpdateInput) (Event, error) {
	if err := access.RequireManage(); err != nil {
		return Event{}, err
	}
	event, err := s.load(ctx, access.Trip.ID, eventID)
	if err != nil {
		return Event{}, err
	}
	timeChanged := false

	if in.Title != nil {
		event.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		event.Description = strings.TrimSpace(*in.Description)
	}
	if in.StartAt != nil {
		timeChanged = !in.StartAt.Equal(event.StartAt)
		event.StartAt = core.UTC(*in.StartAt)
	}
	switch {
	case in.ClearEndAt:
		event.EndAt = nil
	case in.EndAt != nil:
		event.EndAt = core.UTCPtr(in.EndAt)
	}
	if in.LocationName != nil {
		event.LocationName = strings.TrimSpace(*in.LocationName)
	}
	if in.Latitude != nil {
		event.Latitude = in.Latitude
	}
	if in.Longitude != nil {
		event.Longitude = in.Longitude
	}
	if in.Type != nil {
		event.Type = *in.Type
	}

	check := CreateInput{
		Title: event.Title, Description: event.Description, StartAt: event.StartAt, EndAt: event.EndAt,
		LocationName: event.LocationName, Latitude: event.Latitude, Longitude: event.Longitude, Type: event.Type,
	}
	if err := check.validate(); err != nil {
		return Event{}, err
	}

	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		updated, err := repo.Update(ctx, event)
		if err != nil {
			return err
		}
		event = updated
		if in.ParticipantIDs != nil {
			roster, err := s.resolveRoster(ctx, access, in.ParticipantIDs)
			if err != nil {
				return err
			}
			return repo.SetParticipants(ctx, event.ID, roster)
		}
		return nil
	})
	if err != nil {
		return Event{}, err
	}

	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindEventUpdated,
		Message: fmt.Sprintf("%s updated %q", access.Member.DisplayName, event.Title),
		Meta:    map[string]any{"event_id": event.ID.String()},
	})
	// A moved event is the one change everybody must know about; a typo fix is not.
	if timeChanged {
		s.fanOut(ctx, access, notify.Notification{
			TripID:   access.Trip.ID,
			Category: notify.CategoryTripUpdates,
			Title:    "📅 " + access.Trip.Title,
			Body: fmt.Sprintf("%s moved to %s", event.Title,
				event.StartAt.In(access.Trip.Location()).Format("2 Jan, 15:04")),
			Payload: map[string]any{"event_id": event.ID.String()},
		})
	}
	return s.withParticipants(ctx, access.Trip.ID, event)
}

func (s *Service) Delete(ctx context.Context, access trips.Access, eventID core.ID) error {
	if err := access.RequireManage(); err != nil {
		return err
	}
	event, err := s.load(ctx, access.Trip.ID, eventID)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, eventID); err != nil {
		return err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindEventDeleted,
		Message: fmt.Sprintf("%s removed %q from the timeline", access.Member.DisplayName, event.Title),
	})
	return nil
}

// List returns the whole timeline with rosters attached.
func (s *Service) List(ctx context.Context, access trips.Access) ([]Event, error) {
	list, err := s.repo.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	rosters, err := s.repo.ParticipantsByEvent(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		attach(&list[i], rosters[list[i].ID])
	}
	return list, nil
}

// Get returns one event with its roster.
func (s *Service) Get(ctx context.Context, access trips.Access, eventID core.ID) (Event, error) {
	event, err := s.load(ctx, access.Trip.ID, eventID)
	if err != nil {
		return Event{}, err
	}
	return s.withParticipants(ctx, access.Trip.ID, event)
}

// SetRSVP records an answer. Members answer for themselves; organisers may
// answer for anyone, which is what makes "chase Sasha by phone and tick the box"
// possible.
func (s *Service) SetRSVP(ctx context.Context, access trips.Access, eventID, memberID core.ID, status RSVP) (Event, error) {
	if memberID.IsZero() {
		memberID = access.Member.ID
	}
	if err := access.RequireSelfOrManage(memberID); err != nil {
		return Event{}, err
	}
	if !status.Valid() {
		return Event{}, core.Invalid("status must be one of: %s", strings.Join(RSVPStrings(), ", "))
	}
	event, err := s.load(ctx, access.Trip.ID, eventID)
	if err != nil {
		return Event{}, err
	}
	member, err := s.trips.MemberByID(ctx, access.Trip.ID, memberID)
	if err != nil {
		return Event{}, err
	}
	if err := s.repo.SetRSVP(ctx, eventID, memberID, status); err != nil {
		return Event{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindEventRSVP,
		Message: fmt.Sprintf("%s is %s %q", member.DisplayName, rsvpVerb(status), event.Title),
		Meta:    map[string]any{"event_id": event.ID.String(), "status": string(status)},
	})
	return s.withParticipants(ctx, access.Trip.ID, event)
}

func rsvpVerb(s RSVP) string {
	switch s {
	case RSVPAttending:
		return "attending"
	case RSVPNotAttending:
		return "not attending"
	case RSVPMaybe:
		return "unsure about"
	default:
		return "undecided about"
	}
}

// ------------------------------------------------------------------ helpers --

func (s *Service) load(ctx context.Context, tripID, eventID core.ID) (Event, error) {
	event, err := s.repo.ByID(ctx, eventID)
	if err != nil {
		return Event{}, err
	}
	if event.TripID != tripID {
		return Event{}, core.NotFound("event")
	}
	return event, nil
}

func (s *Service) withParticipants(ctx context.Context, tripID core.ID, event Event) (Event, error) {
	rosters, err := s.repo.ParticipantsByEvent(ctx, tripID)
	if err != nil {
		return Event{}, err
	}
	attach(&event, rosters[event.ID])
	return event, nil
}

func attach(e *Event, roster []Participant) {
	e.Participants = roster
	e.Attending, e.Undecided = 0, 0
	for _, p := range roster {
		switch p.Status {
		case RSVPAttending:
			e.Attending++
		case RSVPUndecided:
			e.Undecided++
		}
	}
	if e.Participants == nil {
		e.Participants = []Participant{}
	}
}

// resolveRoster turns requested participant ids into validated member ids,
// defaulting to the whole active group.
func (s *Service) resolveRoster(ctx context.Context, access trips.Access, requested []core.ID) ([]core.ID, error) {
	members, err := s.trips.Members(ctx, access)
	if err != nil {
		return nil, err
	}
	active := map[core.ID]bool{}
	all := make([]core.ID, 0, len(members))
	for _, m := range members {
		if m.Active() {
			active[m.ID] = true
			all = append(all, m.ID)
		}
	}
	if len(requested) == 0 {
		return all, nil
	}
	out := make([]core.ID, 0, len(requested))
	seen := map[core.ID]bool{}
	for _, id := range requested {
		if !active[id] {
			return nil, core.Invalid("participant_ids contains someone who is not on this trip")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// fanOut notifies every active member except the actor.
func (s *Service) fanOut(ctx context.Context, access trips.Access, template notify.Notification) {
	userIDs, err := s.trips.ActiveMemberUserIDs(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	items := make([]notify.Notification, 0, len(userIDs))
	for _, id := range userIDs {
		if id == access.User.ID {
			continue
		}
		n := template
		n.UserID = id
		items = append(items, n)
	}
	_ = s.notify.Enqueue(ctx, items...)
}
