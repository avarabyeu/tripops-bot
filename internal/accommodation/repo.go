package accommodation

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
)

type Repo struct{ db *gorm.DB }

func NewRepo(gdb *gorm.DB) *Repo { return &Repo{db: gdb} }

func (r *Repo) Insert(ctx context.Context, a Accommodation) (Accommodation, error) {
	now := time.Now().UTC()
	a.CreatedAt, a.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&a).Error; err != nil {
		return Accommodation{}, core.Internal(fmt.Errorf("accommodation: insert: %w", err))
	}
	return a, nil
}

func (r *Repo) Update(ctx context.Context, a Accommodation) (Accommodation, error) {
	res := r.db.WithContext(ctx).Model(&Accommodation{}).Where("id = ?", a.ID).
		Updates(map[string]any{
			"name":       a.Name,
			"address":    a.Address,
			"url":        a.URL,
			"check_in":   a.CheckIn,
			"check_out":  a.CheckOut,
			"capacity":   a.Capacity,
			"notes":      a.Notes,
			"updated_at": time.Now().UTC(),
		})
	if res.Error != nil {
		return Accommodation{}, core.Internal(fmt.Errorf("accommodation: update: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Accommodation{}, core.NotFound("accommodation")
	}
	return r.ByID(ctx, a.ID)
}

func (r *Repo) ByID(ctx context.Context, id core.ID) (Accommodation, error) {
	var a Accommodation
	err := r.db.WithContext(ctx).First(&a, "id = ?", id).Error
	if db.IsNotFound(err) {
		return Accommodation{}, core.NotFound("accommodation")
	}
	if err != nil {
		return Accommodation{}, core.Internal(fmt.Errorf("accommodation: by id: %w", err))
	}
	return a, nil
}

func (r *Repo) Delete(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Delete(&Accommodation{}, "id = ?", id)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("accommodation: delete: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("accommodation")
	}
	return nil
}

func (r *Repo) ListByTrip(ctx context.Context, tripID core.ID) ([]Accommodation, error) {
	out := []Accommodation{}
	err := r.db.WithContext(ctx).Where("trip_id = ?", tripID).
		Order("check_in, created_at").Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("accommodation: list: %w", err))
	}
	return out, nil
}

// GuestsByAccommodation loads the guest lists of a whole trip at once.
func (r *Repo) GuestsByAccommodation(ctx context.Context, tripID core.ID) (map[core.ID][]Guest, error) {
	type row struct {
		AccommodationID core.ID
		MemberID        core.ID
		DisplayName     string
		Status          GuestStatus
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("accommodation_guests AS g").
		Select("g.accommodation_id, g.member_id, m.display_name, g.status").
		Joins("JOIN accommodations a ON a.id = g.accommodation_id").
		Joins("JOIN trip_members m ON m.id = g.member_id").
		Where("a.trip_id = ? AND m.status <> ?", tripID, "removed").
		Order("m.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("accommodation: guests: %w", err))
	}
	out := map[core.ID][]Guest{}
	for _, r := range rows {
		out[r.AccommodationID] = append(out[r.AccommodationID], Guest{
			MemberID: r.MemberID, DisplayName: r.DisplayName, Status: r.Status,
		})
	}
	return out, nil
}

// SetGuests replaces the guest list, keeping the status of guests who stay on it.
func (r *Repo) SetGuests(ctx context.Context, accommodationID core.ID, memberIDs []core.ID) error {
	keep := core.IDStrings(memberIDs)
	pruned := r.db.WithContext(ctx).Where("accommodation_id = ?", accommodationID)
	if len(keep) > 0 {
		pruned = pruned.Where("member_id NOT IN ?", keep)
	}
	if err := pruned.Delete(&guestRow{}).Error; err != nil {
		return core.Internal(fmt.Errorf("accommodation: prune guests: %w", err))
	}
	if len(memberIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]guestRow, 0, len(memberIDs))
	for _, id := range memberIDs {
		rows = append(rows, guestRow{
			AccommodationID: accommodationID, MemberID: id, Status: GuestPending, CreatedAt: now,
		})
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	if err != nil {
		return core.Internal(fmt.Errorf("accommodation: add guests: %w", err))
	}
	return nil
}

func (r *Repo) SetGuestStatus(ctx context.Context, accommodationID, memberID core.ID, status GuestStatus) error {
	row := guestRow{
		AccommodationID: accommodationID, MemberID: memberID,
		Status: status, CreatedAt: time.Now().UTC(),
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "accommodation_id"}, {Name: "member_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"status"}),
	}).Create(&row).Error
	if db.IsForeignKeyViolation(err) {
		return core.NotFound("accommodation or member")
	}
	if err != nil {
		return core.Internal(fmt.Errorf("accommodation: set guest status: %w", err))
	}
	return nil
}

// TripCounts is the dashboard summary: confirmed beds out of claimed beds.
type TripCounts struct {
	Places    int `json:"places"`
	Confirmed int `json:"confirmed"`
	Total     int `json:"total"`
}

func (r *Repo) Counts(ctx context.Context, tripID core.ID) (TripCounts, error) {
	var c TripCounts
	var places int64
	if err := r.db.WithContext(ctx).Model(&Accommodation{}).
		Where("trip_id = ?", tripID).Count(&places).Error; err != nil {
		return TripCounts{}, core.Internal(fmt.Errorf("accommodation: count places: %w", err))
	}
	c.Places = int(places)

	err := r.db.WithContext(ctx).
		Table("accommodation_guests AS g").
		Select(`count(CASE WHEN g.status = 'confirmed' THEN 1 END) AS confirmed,
			count(CASE WHEN g.status <> 'declined' THEN 1 END) AS total`).
		Joins("JOIN accommodations a ON a.id = g.accommodation_id").
		Where("a.trip_id = ?", tripID).
		Scan(&c).Error
	if err != nil {
		return TripCounts{}, core.Internal(fmt.Errorf("accommodation: counts: %w", err))
	}
	c.Places = int(places)
	return c, nil
}

// PendingGuest is somebody who has a bed but has not confirmed it, which is
// the "Sasha has not confirmed accommodation" rule.
type PendingGuest struct {
	AccommodationID   core.ID
	AccommodationName string
	MemberID          core.ID
	UserID            core.ID
	DisplayName       string
}

func (r *Repo) PendingGuests(ctx context.Context, tripID core.ID) ([]PendingGuest, error) {
	out := []PendingGuest{}
	err := r.db.WithContext(ctx).
		Table("accommodation_guests AS g").
		Select(`a.id AS accommodation_id, a.name AS accommodation_name,
			m.id AS member_id, m.user_id AS user_id, m.display_name AS display_name`).
		Joins("JOIN accommodations a ON a.id = g.accommodation_id").
		Joins("JOIN trip_members m ON m.id = g.member_id").
		Where("a.trip_id = ? AND g.status = ? AND m.status = ?", tripID, string(GuestPending), "active").
		Order("m.created_at").
		Scan(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("accommodation: pending guests: %w", err))
	}
	return out, nil
}
