// Package activity records a lightweight, human readable feed of what happened
// on a trip ("Vasya joined the trip", "AV created Departure").
//
// It is deliberately not an audit trail: entries are sentences meant for the
// group, not structured diffs meant for an operator. Writing an entry must
// never be able to fail a business operation, so every write here swallows its
// error into the process log.
package activity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Kinds used across the modules. They are strings rather than an enum so a new
// module can add one without a migration, but keeping the known set listed
// here keeps the feed vocabulary consistent.
const (
	KindMemberJoined      = "member.joined"
	KindMemberLeft        = "member.left"
	KindMemberRoleChanged = "member.role_changed"
	KindTripCreated       = "trip.created"
	KindTripUpdated       = "trip.updated"
	KindEventCreated      = "event.created"
	KindEventUpdated      = "event.updated"
	KindEventDeleted      = "event.deleted"
	KindEventRSVP         = "event.rsvp"
	KindDecisionCreated   = "decision.created"
	KindDecisionVoted     = "decision.voted"
	KindDecisionClosed    = "decision.closed"
	KindDecisionResolved  = "decision.resolved"
	KindVehicleUpdated    = "vehicle.updated"
	KindAccommodation     = "accommodation.updated"
	KindChecklistUpdated  = "checklist.updated"
	KindChecklistItemDone = "checklist.item_completed"
	KindExpenseAdded      = "expense.added"
	KindExpenseUpdated    = "expense.updated"
	KindExpenseDeleted    = "expense.deleted"
	KindSettlementMarked  = "settlement.settled"
)

// Entry is one line of the feed.
type Entry struct {
	ID            core.ID      `json:"id"                        gorm:"primaryKey"`
	TripID        core.ID      `json:"trip_id"                   gorm:"not null;index:idx_activity_trip,priority:1"`
	ActorUserID   core.ID      `json:"-"`
	ActorMemberID core.ID      `json:"actor_member_id,omitempty"`
	ActorName     string       `json:"actor_name"                gorm:"size:120;not null;default:''"`
	Kind          string       `json:"kind"                      gorm:"size:64;not null"`
	Message       string       `json:"message"                   gorm:"size:500;not null"`
	Meta          core.JSONMap `json:"meta,omitempty"`
	CreatedAt     time.Time    `json:"created_at"                gorm:"not null;index:idx_activity_trip,priority:2,sort:desc"`
}

func (Entry) TableName() string { return "activity_log" }

// Logger is the narrow interface domain services depend on.
type Logger interface {
	Log(ctx context.Context, e Entry)
}

// Service is the database backed Logger.
type Service struct {
	db  *gorm.DB
	log *slog.Logger
}

func NewService(gdb *gorm.DB, log *slog.Logger) *Service {
	return &Service{db: gdb, log: log}
}

// Log appends an entry. Errors are logged and dropped on purpose: a failure to
// narrate must not roll back the thing being narrated.
func (s *Service) Log(ctx context.Context, e Entry) {
	if e.TripID.IsZero() || e.Message == "" {
		return
	}
	e.ID = core.NewID()
	e.CreatedAt = time.Now().UTC()
	if err := s.db.WithContext(ctx).Create(&e).Error; err != nil && s.log != nil {
		s.log.WarnContext(ctx, "activity log write failed", "kind", e.Kind, "err", err)
	}
}

// List returns the most recent entries for a trip, newest first.
func (s *Service) List(ctx context.Context, tripID core.ID, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []Entry{}
	err := s.db.WithContext(ctx).
		Where("trip_id = ?", tripID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("activity: list: %w", err))
	}
	return out, nil
}

// Nop is a Logger that discards everything, for tests.
type Nop struct{}

func (Nop) Log(context.Context, Entry) {}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Entry{}} }
