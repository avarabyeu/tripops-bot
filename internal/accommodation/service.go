package accommodation

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

type Service struct {
	database *db.DB
	repo     *Repo
	trips    *trips.Service
	activity activity.Logger
}

func NewService(database *db.DB, tripSvc *trips.Service, act activity.Logger) *Service {
	return &Service{database: database, repo: NewRepo(database.DB), trips: tripSvc, activity: act}
}

func (s *Service) Repo() *Repo { return s.repo }

// Input is a place to stay. GuestIDs defaults to nobody: who sleeps where is a
// separate, deliberate act.
type Input struct {
	Name     string     `json:"name"`
	Address  string     `json:"address"`
	URL      string     `json:"url"`
	CheckIn  *time.Time `json:"check_in"`
	CheckOut *time.Time `json:"check_out"`
	Capacity int        `json:"capacity"`
	Notes    string     `json:"notes"`
	GuestIDs []core.ID  `json:"guest_ids"`
}

func (in *Input) normalise() {
	in.Name = strings.TrimSpace(in.Name)
	in.Address = strings.TrimSpace(in.Address)
	in.URL = strings.TrimSpace(in.URL)
	in.Notes = strings.TrimSpace(in.Notes)
	in.CheckIn = core.UTCPtr(in.CheckIn)
	in.CheckOut = core.UTCPtr(in.CheckOut)
}

func (in Input) validate() error {
	v := core.NewValidator()
	v.Required(in.Name, "name")
	v.Length(in.Name, "name", 1, 120)
	v.Length(in.Address, "address", 0, 300)
	v.Length(in.Notes, "notes", 0, 1000)
	v.Check(in.Capacity >= 0 && in.Capacity <= 200, "capacity", "must be between 0 and 200")
	if in.URL != "" {
		parsed, err := url.Parse(in.URL)
		v.Check(err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "",
			"url", "must be an http(s) link")
	}
	if in.CheckIn != nil && in.CheckOut != nil {
		v.Check(!in.CheckOut.Before(*in.CheckIn), "check_out", "must not be before check-in")
	}
	return v.Err()
}

func (s *Service) List(ctx context.Context, access trips.Access) ([]Accommodation, error) {
	list, err := s.repo.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	guests, err := s.repo.GuestsByAccommodation(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Guests = guests[list[i].ID]
		list[i].recompute()
	}
	return list, nil
}

func (s *Service) Create(ctx context.Context, access trips.Access, in Input) (Accommodation, error) {
	if err := access.RequireManage(); err != nil {
		return Accommodation{}, err
	}
	in.normalise()
	if err := in.validate(); err != nil {
		return Accommodation{}, err
	}
	guests, err := s.resolveGuests(ctx, access, in.GuestIDs)
	if err != nil {
		return Accommodation{}, err
	}
	if in.Capacity > 0 && len(guests) > in.Capacity {
		return Accommodation{}, core.Conflict("that is %d guests for %d beds", len(guests), in.Capacity)
	}

	place := Accommodation{
		ID: core.NewID(), TripID: access.Trip.ID, Name: in.Name, Address: in.Address, URL: in.URL,
		CheckIn: in.CheckIn, CheckOut: in.CheckOut, Capacity: in.Capacity, Notes: in.Notes,
	}
	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.Insert(ctx, place)
		if err != nil {
			return err
		}
		place = created
		return repo.SetGuests(ctx, place.ID, guests)
	})
	if err != nil {
		return Accommodation{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindAccommodation,
		Message: fmt.Sprintf("%s added %s", access.Member.DisplayName, place.Name),
	})
	return s.get(ctx, access, place.ID)
}

// Patch updates a place; nil fields are untouched.
type Patch struct {
	Name          *string    `json:"name"`
	Address       *string    `json:"address"`
	URL           *string    `json:"url"`
	CheckIn       *time.Time `json:"check_in"`
	CheckOut      *time.Time `json:"check_out"`
	ClearCheckIn  bool       `json:"clear_check_in"`
	ClearCheckOut bool       `json:"clear_check_out"`
	Capacity      *int       `json:"capacity"`
	Notes         *string    `json:"notes"`
	GuestIDs      *[]core.ID `json:"guest_ids"`
}

func (s *Service) Update(ctx context.Context, access trips.Access, id core.ID, in Patch) (Accommodation, error) {
	if err := access.RequireManage(); err != nil {
		return Accommodation{}, err
	}
	place, err := s.load(ctx, access.Trip.ID, id)
	if err != nil {
		return Accommodation{}, err
	}
	if in.Name != nil {
		place.Name = strings.TrimSpace(*in.Name)
	}
	if in.Address != nil {
		place.Address = strings.TrimSpace(*in.Address)
	}
	if in.URL != nil {
		place.URL = strings.TrimSpace(*in.URL)
	}
	if in.Capacity != nil {
		place.Capacity = *in.Capacity
	}
	if in.Notes != nil {
		place.Notes = strings.TrimSpace(*in.Notes)
	}
	if in.ClearCheckIn {
		place.CheckIn = nil
	} else if in.CheckIn != nil {
		place.CheckIn = core.UTCPtr(in.CheckIn)
	}
	if in.ClearCheckOut {
		place.CheckOut = nil
	} else if in.CheckOut != nil {
		place.CheckOut = core.UTCPtr(in.CheckOut)
	}

	check := Input{
		Name: place.Name, Address: place.Address, URL: place.URL, CheckIn: place.CheckIn,
		CheckOut: place.CheckOut, Capacity: place.Capacity, Notes: place.Notes,
	}
	if err := check.validate(); err != nil {
		return Accommodation{}, err
	}

	var guests []core.ID
	if in.GuestIDs != nil {
		guests, err = s.resolveGuests(ctx, access, *in.GuestIDs)
		if err != nil {
			return Accommodation{}, err
		}
		if place.Capacity > 0 && len(guests) > place.Capacity {
			return Accommodation{}, core.Conflict("that is %d guests for %d beds", len(guests), place.Capacity)
		}
	}

	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		updated, err := repo.Update(ctx, place)
		if err != nil {
			return err
		}
		place = updated
		if in.GuestIDs != nil {
			return repo.SetGuests(ctx, place.ID, guests)
		}
		return nil
	})
	if err != nil {
		return Accommodation{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindAccommodation,
		Message: fmt.Sprintf("%s updated %s", access.Member.DisplayName, place.Name),
	})
	return s.get(ctx, access, place.ID)
}

func (s *Service) Delete(ctx context.Context, access trips.Access, id core.ID) error {
	if err := access.RequireManage(); err != nil {
		return err
	}
	place, err := s.load(ctx, access.Trip.ID, id)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindAccommodation,
		Message: fmt.Sprintf("%s removed %s", access.Member.DisplayName, place.Name),
	})
	return nil
}

// SetGuestStatus confirms or declines a bed. Members answer for themselves,
// organisers for anyone.
func (s *Service) SetGuestStatus(ctx context.Context, access trips.Access, id, memberID core.ID, status GuestStatus) (Accommodation, error) {
	if memberID.IsZero() {
		memberID = access.Member.ID
	}
	if err := access.RequireSelfOrManage(memberID); err != nil {
		return Accommodation{}, err
	}
	if !status.Valid() {
		return Accommodation{}, core.Invalid("status must be one of: %s", strings.Join(GuestStatusStrings(), ", "))
	}
	place, err := s.load(ctx, access.Trip.ID, id)
	if err != nil {
		return Accommodation{}, err
	}
	if _, err := s.trips.MemberByID(ctx, access.Trip.ID, memberID); err != nil {
		return Accommodation{}, err
	}

	// Confirming a bed must respect capacity; declining never can overflow.
	if status != GuestDeclined && place.Capacity > 0 {
		current, err := s.get(ctx, access, id)
		if err != nil {
			return Accommodation{}, err
		}
		already := false
		for _, g := range current.Guests {
			if g.MemberID == memberID {
				already = g.Status != GuestDeclined
			}
		}
		if !already && current.Occupied >= place.Capacity {
			return Accommodation{}, core.Conflict("%s is full (%d beds)", place.Name, place.Capacity)
		}
	}

	if err := s.repo.SetGuestStatus(ctx, id, memberID, status); err != nil {
		return Accommodation{}, err
	}
	return s.get(ctx, access, id)
}

// ----------------------------------------------------------------- helpers --

func (s *Service) resolveGuests(ctx context.Context, access trips.Access, requested []core.ID) ([]core.ID, error) {
	if len(requested) == 0 {
		return []core.ID{}, nil
	}
	members, err := s.trips.Members(ctx, access)
	if err != nil {
		return nil, err
	}
	active := map[core.ID]bool{}
	for _, m := range members {
		if m.Active() {
			active[m.ID] = true
		}
	}
	out := make([]core.ID, 0, len(requested))
	seen := map[core.ID]bool{}
	for _, id := range requested {
		if !active[id] {
			return nil, core.Invalid("guest_ids contains someone who is not on this trip")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func (s *Service) load(ctx context.Context, tripID, id core.ID) (Accommodation, error) {
	place, err := s.repo.ByID(ctx, id)
	if err != nil {
		return Accommodation{}, err
	}
	if place.TripID != tripID {
		return Accommodation{}, core.NotFound("accommodation")
	}
	return place, nil
}

func (s *Service) get(ctx context.Context, access trips.Access, id core.ID) (Accommodation, error) {
	list, err := s.List(ctx, access)
	if err != nil {
		return Accommodation{}, err
	}
	for _, a := range list {
		if a.ID == id {
			return a, nil
		}
	}
	return Accommodation{}, core.NotFound("accommodation")
}
