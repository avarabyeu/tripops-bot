package logistics

import (
	"context"
	"fmt"
	"strings"

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

// VehicleInput is the whole vehicle: seating is edited together with the car
// itself, because "add a car" and "put people in it" are one thought.
type VehicleInput struct {
	Name           string    `json:"name"`
	Type           Type      `json:"type"`
	Capacity       int       `json:"capacity"`
	DriverMemberID core.ID   `json:"driver_member_id"`
	PassengerIDs   []core.ID `json:"passenger_ids"`
	Notes          string    `json:"notes"`
}

func (in *VehicleInput) normalise() {
	in.Name = strings.TrimSpace(in.Name)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.Type == "" {
		in.Type = TypeCar
	}
}

func (in VehicleInput) validate() error {
	v := core.NewValidator()
	v.Required(in.Name, "name")
	v.Length(in.Name, "name", 1, 80)
	v.Length(in.Notes, "notes", 0, 500)
	v.Check(in.Type.Valid(), "type", "must be one of: "+strings.Join(TypeStrings(), ", "))
	v.Check(in.Capacity >= 1 && in.Capacity <= 100, "capacity", "must be between 1 and 100")
	return v.Err()
}

func (s *Service) List(ctx context.Context, access trips.Access) ([]Vehicle, error) {
	list, err := s.repo.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	seating, err := s.repo.PassengersByVehicle(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Passengers = seating[list[i].ID]
		list[i].recompute()
	}
	return list, nil
}

func (s *Service) Create(ctx context.Context, access trips.Access, in VehicleInput) (Vehicle, error) {
	if err := access.RequireManage(); err != nil {
		return Vehicle{}, err
	}
	in.normalise()
	if err := in.validate(); err != nil {
		return Vehicle{}, err
	}
	vehicle := Vehicle{
		ID: core.NewID(), TripID: access.Trip.ID, Name: in.Name, Type: in.Type,
		Capacity: in.Capacity, DriverMemberID: in.DriverMemberID, Notes: in.Notes,
	}
	if err := s.checkSeating(ctx, access, vehicle.ID, in.Capacity, in.DriverMemberID, in.PassengerIDs); err != nil {
		return Vehicle{}, err
	}

	err := s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.Insert(ctx, vehicle)
		if err != nil {
			return err
		}
		vehicle = created
		return repo.SetPassengers(ctx, vehicle.ID, in.PassengerIDs)
	})
	if err != nil {
		return Vehicle{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindVehicleUpdated,
		Message: fmt.Sprintf("%s added %s", access.Member.DisplayName, vehicle.Name),
	})
	return s.get(ctx, access, vehicle.ID)
}

// VehiclePatch updates a vehicle; nil fields are left alone.
type VehiclePatch struct {
	Name           *string    `json:"name"`
	Type           *Type      `json:"type"`
	Capacity       *int       `json:"capacity"`
	DriverMemberID *core.ID   `json:"driver_member_id"`
	ClearDriver    bool       `json:"clear_driver"`
	PassengerIDs   *[]core.ID `json:"passenger_ids"`
	Notes          *string    `json:"notes"`
}

func (s *Service) Update(ctx context.Context, access trips.Access, vehicleID core.ID, in VehiclePatch) (Vehicle, error) {
	if err := access.RequireManage(); err != nil {
		return Vehicle{}, err
	}
	vehicle, err := s.load(ctx, access.Trip.ID, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}
	passengers, err := s.repo.PassengerIDs(ctx, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}

	if in.Name != nil {
		vehicle.Name = strings.TrimSpace(*in.Name)
	}
	if in.Type != nil {
		vehicle.Type = *in.Type
	}
	if in.Capacity != nil {
		vehicle.Capacity = *in.Capacity
	}
	if in.Notes != nil {
		vehicle.Notes = strings.TrimSpace(*in.Notes)
	}
	switch {
	case in.ClearDriver:
		vehicle.DriverMemberID = core.Nil
	case in.DriverMemberID != nil:
		vehicle.DriverMemberID = *in.DriverMemberID
	}
	if in.PassengerIDs != nil {
		passengers = *in.PassengerIDs
	}

	check := VehicleInput{Name: vehicle.Name, Type: vehicle.Type, Capacity: vehicle.Capacity, Notes: vehicle.Notes}
	if err := check.validate(); err != nil {
		return Vehicle{}, err
	}
	if err := s.checkSeating(ctx, access, vehicle.ID, vehicle.Capacity, vehicle.DriverMemberID, passengers); err != nil {
		return Vehicle{}, err
	}

	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		updated, err := repo.Update(ctx, vehicle)
		if err != nil {
			return err
		}
		vehicle = updated
		if in.PassengerIDs != nil {
			return repo.SetPassengers(ctx, vehicle.ID, passengers)
		}
		return nil
	})
	if err != nil {
		return Vehicle{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindVehicleUpdated,
		Message: fmt.Sprintf("%s updated %s", access.Member.DisplayName, vehicle.Name),
	})
	return s.get(ctx, access, vehicle.ID)
}

func (s *Service) Delete(ctx context.Context, access trips.Access, vehicleID core.ID) error {
	if err := access.RequireManage(); err != nil {
		return err
	}
	vehicle, err := s.load(ctx, access.Trip.ID, vehicleID)
	if err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, vehicleID); err != nil {
		return err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindVehicleUpdated,
		Message: fmt.Sprintf("%s removed %s", access.Member.DisplayName, vehicle.Name),
	})
	return nil
}

// Join puts the calling member in a vehicle, which is the one seating change a
// plain member is allowed to make for themselves.
func (s *Service) Join(ctx context.Context, access trips.Access, vehicleID core.ID) (Vehicle, error) {
	if err := access.RequireWrite(); err != nil {
		return Vehicle{}, err
	}
	vehicle, err := s.load(ctx, access.Trip.ID, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}
	passengers, err := s.repo.PassengerIDs(ctx, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}
	for _, id := range passengers {
		if id == access.Member.ID {
			return s.get(ctx, access, vehicleID)
		}
	}
	if vehicle.DriverMemberID == access.Member.ID {
		return s.get(ctx, access, vehicleID)
	}
	passengers = append(passengers, access.Member.ID)
	if err := s.checkSeating(ctx, access, vehicleID, vehicle.Capacity, vehicle.DriverMemberID, passengers); err != nil {
		return Vehicle{}, err
	}
	if err := s.repo.SetPassengers(ctx, vehicleID, passengers); err != nil {
		return Vehicle{}, err
	}
	return s.get(ctx, access, vehicleID)
}

// Leave removes the calling member from a vehicle.
func (s *Service) Leave(ctx context.Context, access trips.Access, vehicleID core.ID) (Vehicle, error) {
	if err := access.RequireWrite(); err != nil {
		return Vehicle{}, err
	}
	if _, err := s.load(ctx, access.Trip.ID, vehicleID); err != nil {
		return Vehicle{}, err
	}
	passengers, err := s.repo.PassengerIDs(ctx, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}
	kept := make([]core.ID, 0, len(passengers))
	for _, id := range passengers {
		if id != access.Member.ID {
			kept = append(kept, id)
		}
	}
	if err := s.repo.SetPassengers(ctx, vehicleID, kept); err != nil {
		return Vehicle{}, err
	}
	return s.get(ctx, access, vehicleID)
}

// ----------------------------------------------------------------- helpers --

// checkSeating enforces the two seating rules: everyone must be an active
// member of this trip and must not already have a seat in another vehicle, and
// the occupants (driver included) must fit.
func (s *Service) checkSeating(ctx context.Context, access trips.Access, vehicleID core.ID, capacity int, driver core.ID, passengers []core.ID) error {
	members, err := s.trips.Members(ctx, access)
	if err != nil {
		return err
	}
	active := map[core.ID]string{}
	for _, m := range members {
		if m.Active() {
			active[m.ID] = m.DisplayName
		}
	}
	occupants := make([]core.ID, 0, len(passengers)+1)
	if !driver.IsZero() {
		if _, ok := active[driver]; !ok {
			return core.Invalid("the driver must be an active participant of this trip")
		}
		occupants = append(occupants, driver)
	}
	seen := map[core.ID]bool{}
	for _, id := range passengers {
		if _, ok := active[id]; !ok {
			return core.Invalid("passenger_ids contains someone who is not on this trip")
		}
		if seen[id] {
			return core.Invalid("%s is listed twice", active[id])
		}
		seen[id] = true
		occupants = append(occupants, id)
	}

	if !FitsCapacity(capacity, driver, passengers) {
		return core.Conflict("that is %d people in %d seats — the driver takes a seat too",
			SeatsTaken(driver, passengers), capacity)
	}

	elsewhere, err := s.repo.SeatedElsewhere(ctx, access.Trip.ID, vehicleID, occupants)
	if err != nil {
		return err
	}
	for id, vehicleName := range elsewhere {
		return core.Conflict("%s is already travelling in %s", active[id], vehicleName)
	}
	return nil
}

func (s *Service) load(ctx context.Context, tripID, vehicleID core.ID) (Vehicle, error) {
	vehicle, err := s.repo.ByID(ctx, vehicleID)
	if err != nil {
		return Vehicle{}, err
	}
	if vehicle.TripID != tripID {
		return Vehicle{}, core.NotFound("vehicle")
	}
	return vehicle, nil
}

func (s *Service) get(ctx context.Context, access trips.Access, vehicleID core.ID) (Vehicle, error) {
	list, err := s.List(ctx, access)
	if err != nil {
		return Vehicle{}, err
	}
	for _, v := range list {
		if v.ID == vehicleID {
			return v, nil
		}
	}
	return Vehicle{}, core.NotFound("vehicle")
}
