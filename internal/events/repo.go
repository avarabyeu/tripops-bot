package events

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

func (r *Repo) Insert(ctx context.Context, e Event) (Event, error) {
	now := time.Now().UTC()
	e.CreatedAt, e.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&e).Error; err != nil {
		return Event{}, core.Internal(fmt.Errorf("events: insert: %w", err))
	}
	return e, nil
}

func (r *Repo) Update(ctx context.Context, e Event) (Event, error) {
	e.UpdatedAt = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&Event{}).Where("id = ?", e.ID).
		Updates(map[string]any{
			"title":         e.Title,
			"description":   e.Description,
			"start_at":      e.StartAt,
			"end_at":        e.EndAt,
			"location_name": e.LocationName,
			"latitude":      e.Latitude,
			"longitude":     e.Longitude,
			"type":          string(e.Type),
			"updated_at":    e.UpdatedAt,
		})
	if res.Error != nil {
		return Event{}, core.Internal(fmt.Errorf("events: update: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Event{}, core.NotFound("event")
	}
	return r.ByID(ctx, e.ID)
}

func (r *Repo) ByID(ctx context.Context, id core.ID) (Event, error) {
	var e Event
	err := r.db.WithContext(ctx).First(&e, "id = ?", id).Error
	if db.IsNotFound(err) {
		return Event{}, core.NotFound("event")
	}
	if err != nil {
		return Event{}, core.Internal(fmt.Errorf("events: by id: %w", err))
	}
	return e, nil
}

func (r *Repo) Delete(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Delete(&Event{}, "id = ?", id)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("events: delete: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("event")
	}
	return nil
}

// ListByTrip returns the timeline in chronological order.
func (r *Repo) ListByTrip(ctx context.Context, tripID core.ID) ([]Event, error) {
	out := []Event{}
	err := r.db.WithContext(ctx).Where("trip_id = ?", tripID).
		Order("start_at, created_at").Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("events: list: %w", err))
	}
	return out, nil
}

// NextAfter is the dashboard's "what is coming up" query.
func (r *Repo) NextAfter(ctx context.Context, tripID core.ID, after time.Time) (Event, error) {
	var e Event
	err := r.db.WithContext(ctx).
		Where("trip_id = ? AND start_at >= ?", tripID, after).
		Order("start_at").First(&e).Error
	if db.IsNotFound(err) {
		return Event{}, core.NotFound("event")
	}
	if err != nil {
		return Event{}, core.Internal(fmt.Errorf("events: next: %w", err))
	}
	return e, nil
}

// InRange lists events starting inside [from, to), used by reminders.
func (r *Repo) InRange(ctx context.Context, tripID core.ID, from, to time.Time) ([]Event, error) {
	out := []Event{}
	err := r.db.WithContext(ctx).
		Where("trip_id = ? AND start_at >= ? AND start_at < ?", tripID, from, to).
		Order("start_at").Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("events: in range: %w", err))
	}
	return out, nil
}

// HasType reports whether the trip already has an event of a given type, which
// the attention engine uses to spot a trip with no departure.
func (r *Repo) HasType(ctx context.Context, tripID core.ID, t Type) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Event{}).
		Where("trip_id = ? AND type = ?", tripID, string(t)).Count(&count).Error
	if err != nil {
		return false, core.Internal(fmt.Errorf("events: has type: %w", err))
	}
	return count > 0, nil
}

// ------------------------------------------------------------ participants --

// SetParticipants replaces the roster of an event, keeping any answer that was
// already recorded for a member who stays on it.
func (r *Repo) SetParticipants(ctx context.Context, eventID core.ID, memberIDs []core.ID) error {
	keep := core.IDStrings(memberIDs)
	pruned := r.db.WithContext(ctx).Where("event_id = ?", eventID)
	if len(keep) > 0 {
		pruned = pruned.Where("member_id NOT IN ?", keep)
	}
	if err := pruned.Delete(&participantRow{}).Error; err != nil {
		return core.Internal(fmt.Errorf("events: prune participants: %w", err))
	}
	if len(memberIDs) == 0 {
		return nil
	}

	now := time.Now().UTC()
	rows := make([]participantRow, 0, len(memberIDs))
	for _, id := range memberIDs {
		rows = append(rows, participantRow{
			EventID: eventID, MemberID: id, Status: RSVPUndecided, CreatedAt: now,
		})
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error
	if err != nil {
		return core.Internal(fmt.Errorf("events: add participants: %w", err))
	}
	return nil
}

// SetRSVP records one member's answer.
func (r *Repo) SetRSVP(ctx context.Context, eventID, memberID core.ID, status RSVP) error {
	now := time.Now().UTC()
	row := participantRow{
		EventID: eventID, MemberID: memberID, Status: status,
		RespondedAt: &now, CreatedAt: now,
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_id"}, {Name: "member_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"status", "responded_at"}),
	}).Create(&row).Error
	if db.IsForeignKeyViolation(err) {
		return core.NotFound("event or member")
	}
	if err != nil {
		return core.Internal(fmt.Errorf("events: set rsvp: %w", err))
	}
	return nil
}

// ParticipantsByEvent loads the rosters of every event of a trip in one query.
func (r *Repo) ParticipantsByEvent(ctx context.Context, tripID core.ID) (map[core.ID][]Participant, error) {
	type row struct {
		EventID     core.ID
		MemberID    core.ID
		DisplayName string
		Status      RSVP
		RespondedAt *time.Time
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("event_participants AS ep").
		Select("ep.event_id, ep.member_id, m.display_name, ep.status, ep.responded_at").
		Joins("JOIN events e ON e.id = ep.event_id").
		Joins("JOIN trip_members m ON m.id = ep.member_id").
		Where("e.trip_id = ? AND m.status <> ?", tripID, "removed").
		Order("m.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("events: participants: %w", err))
	}

	out := map[core.ID][]Participant{}
	for _, r := range rows {
		out[r.EventID] = append(out[r.EventID], Participant{
			MemberID: r.MemberID, DisplayName: r.DisplayName,
			Status: r.Status, RespondedAt: r.RespondedAt,
		})
	}
	return out, nil
}

// AttendeeUserIDs lists the users who said they are coming, or have not said
// otherwise, which is who a reminder about the event should reach.
func (r *Repo) AttendeeUserIDs(ctx context.Context, eventID core.ID) ([]core.ID, error) {
	out := []core.ID{}
	err := r.db.WithContext(ctx).
		Table("event_participants AS ep").
		Joins("JOIN trip_members m ON m.id = ep.member_id").
		Where("ep.event_id = ? AND m.status = ? AND ep.status <> ?",
			eventID, "active", string(RSVPNotAttending)).
		Pluck("m.user_id", &out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("events: attendees: %w", err))
	}
	return out, nil
}
