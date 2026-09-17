package logistics

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
)

type Repo struct{ db *gorm.DB }

func NewRepo(gdb *gorm.DB) *Repo { return &Repo{db: gdb} }

// vehicleRow is a vehicle with the driver's name joined in.
type vehicleRow struct {
	Vehicle
	JoinedDriverName string
}

func (row vehicleRow) vehicle() Vehicle {
	v := row.Vehicle
	v.DriverName = row.JoinedDriverName
	return v
}

func (r *Repo) query(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("vehicles AS v").
		Select("v.*, coalesce(d.display_name, '') AS joined_driver_name").
		Joins("LEFT JOIN trip_members d ON d.id = v.driver_member_id")
}

func (r *Repo) Insert(ctx context.Context, v Vehicle) (Vehicle, error) {
	now := time.Now().UTC()
	v.CreatedAt, v.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&v).Error; err != nil {
		return Vehicle{}, core.Internal(fmt.Errorf("logistics: insert vehicle: %w", err))
	}
	return r.ByID(ctx, v.ID)
}

func (r *Repo) Update(ctx context.Context, v Vehicle) (Vehicle, error) {
	res := r.db.WithContext(ctx).Model(&Vehicle{}).Where("id = ?", v.ID).
		Updates(map[string]any{
			"name":             v.Name,
			"type":             string(v.Type),
			"capacity":         v.Capacity,
			"driver_member_id": v.DriverMemberID,
			"notes":            v.Notes,
			"updated_at":       time.Now().UTC(),
		})
	if res.Error != nil {
		return Vehicle{}, core.Internal(fmt.Errorf("logistics: update vehicle: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Vehicle{}, core.NotFound("vehicle")
	}
	return r.ByID(ctx, v.ID)
}

func (r *Repo) ByID(ctx context.Context, id core.ID) (Vehicle, error) {
	var row vehicleRow
	if err := r.query(ctx).Where("v.id = ?", id).Limit(1).Scan(&row).Error; err != nil {
		return Vehicle{}, core.Internal(fmt.Errorf("logistics: vehicle by id: %w", err))
	}
	if row.ID.IsZero() {
		return Vehicle{}, core.NotFound("vehicle")
	}
	return row.vehicle(), nil
}

func (r *Repo) Delete(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Delete(&Vehicle{}, "id = ?", id)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("logistics: delete vehicle: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("vehicle")
	}
	return nil
}

func (r *Repo) ListByTrip(ctx context.Context, tripID core.ID) ([]Vehicle, error) {
	var rows []vehicleRow
	err := r.query(ctx).Where("v.trip_id = ?", tripID).Order("v.created_at").Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("logistics: list vehicles: %w", err))
	}
	out := make([]Vehicle, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.vehicle())
	}
	return out, nil
}

// PassengersByVehicle loads the seating of every vehicle on a trip.
func (r *Repo) PassengersByVehicle(ctx context.Context, tripID core.ID) (map[core.ID][]Passenger, error) {
	type row struct {
		VehicleID   core.ID
		MemberID    core.ID
		DisplayName string
		SeatNote    string
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("vehicle_passengers AS vp").
		Select("vp.vehicle_id, vp.member_id, m.display_name, vp.seat_note").
		Joins("JOIN vehicles v ON v.id = vp.vehicle_id").
		Joins("JOIN trip_members m ON m.id = vp.member_id").
		Where("v.trip_id = ?", tripID).
		Order("m.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("logistics: passengers: %w", err))
	}
	out := map[core.ID][]Passenger{}
	for _, r := range rows {
		out[r.VehicleID] = append(out[r.VehicleID], Passenger{
			MemberID: r.MemberID, DisplayName: r.DisplayName, SeatNote: r.SeatNote,
		})
	}
	return out, nil
}

func (r *Repo) PassengerIDs(ctx context.Context, vehicleID core.ID) ([]core.ID, error) {
	out := []core.ID{}
	err := r.db.WithContext(ctx).Model(&passengerRow{}).
		Where("vehicle_id = ?", vehicleID).Pluck("member_id", &out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("logistics: passenger ids: %w", err))
	}
	return out, nil
}

// SetPassengers replaces the seating of one vehicle.
func (r *Repo) SetPassengers(ctx context.Context, vehicleID core.ID, memberIDs []core.ID) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("vehicle_id = ?", vehicleID).Delete(&passengerRow{}).Error; err != nil {
			return core.Internal(fmt.Errorf("logistics: clear passengers: %w", err))
		}
		if len(memberIDs) == 0 {
			return nil
		}
		now := time.Now().UTC()
		rows := make([]passengerRow, 0, len(memberIDs))
		for _, id := range memberIDs {
			rows = append(rows, passengerRow{VehicleID: vehicleID, MemberID: id, CreatedAt: now})
		}
		if err := tx.Create(&rows).Error; err != nil {
			if db.IsForeignKeyViolation(err) {
				return core.Invalid("passenger is not a member of this trip")
			}
			return core.Internal(fmt.Errorf("logistics: set passengers: %w", err))
		}
		return nil
	})
}

// SeatedElsewhere returns members who already occupy a seat in a vehicle other
// than the one given, as driver or passenger, mapped to that vehicle's name.
func (r *Repo) SeatedElsewhere(ctx context.Context, tripID, exceptVehicleID core.ID, memberIDs []core.ID) (map[core.ID]string, error) {
	out := map[core.ID]string{}
	if len(memberIDs) == 0 {
		return out, nil
	}
	ids := core.IDStrings(memberIDs)

	type row struct {
		MemberID core.ID
		Name     string
	}
	var passengers []row
	err := r.db.WithContext(ctx).
		Table("vehicle_passengers AS vp").
		Select("vp.member_id AS member_id, v.name AS name").
		Joins("JOIN vehicles v ON v.id = vp.vehicle_id").
		Where("v.trip_id = ? AND v.id <> ? AND vp.member_id IN ?", tripID, exceptVehicleID, ids).
		Scan(&passengers).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("logistics: seated elsewhere: %w", err))
	}

	var drivers []row
	err = r.db.WithContext(ctx).
		Table("vehicles AS v").
		Select("v.driver_member_id AS member_id, v.name AS name").
		Where("v.trip_id = ? AND v.id <> ? AND v.driver_member_id IN ?", tripID, exceptVehicleID, ids).
		Scan(&drivers).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("logistics: driving elsewhere: %w", err))
	}

	for _, r := range append(passengers, drivers...) {
		out[r.MemberID] = r.Name
	}
	return out, nil
}

// UnseatedMembers lists active members who have no seat in any vehicle.
func (r *Repo) UnseatedMembers(ctx context.Context, tripID core.ID) ([]core.ID, error) {
	out := []core.ID{}
	err := r.db.WithContext(ctx).
		Table("trip_members AS m").
		Where("m.trip_id = ? AND m.status = ?", tripID, "active").
		Where(`NOT EXISTS (SELECT 1 FROM vehicle_passengers vp
			JOIN vehicles v ON v.id = vp.vehicle_id
			WHERE v.trip_id = ? AND vp.member_id = m.id)`, tripID).
		Where(`NOT EXISTS (SELECT 1 FROM vehicles v
			WHERE v.trip_id = ? AND v.driver_member_id = m.id)`, tripID).
		Pluck("m.id", &out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("logistics: unseated: %w", err))
	}
	return out, nil
}
