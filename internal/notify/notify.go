// Package notify is the notification outbox.
//
// Domain modules enqueue notifications; a delivery worker (the Telegram
// adapter) drains them. That indirection is what keeps domain logic free of
// Telegram, makes delivery retryable, and lets the scheduler run on a dumb
// interval: every scheduled reminder carries a dedupe key, so re-running the
// same rule a minute later is a no-op rather than a second ping.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
)

// Category groups notifications so users can mute them selectively.
type Category string

const (
	CategoryTripUpdates Category = "trip_updates"
	CategoryDecisions   Category = "decisions"
	CategoryReminders   Category = "reminders"
	CategoryChecklist   Category = "checklist"
	CategoryExpenses    Category = "expenses"
)

func (c Category) Valid() bool {
	switch c {
	case CategoryTripUpdates, CategoryDecisions, CategoryReminders, CategoryChecklist, CategoryExpenses:
		return true
	}
	return false
}

// Delivery states of an outbox row.
const (
	StatusPending = "pending"
	StatusSending = "sending"
	StatusSent    = "sent"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// staleClaim is how long a row may sit in "sending" before another worker is
// allowed to pick it up. It only matters when a process dies mid-delivery.
const staleClaim = 5 * time.Minute

// Preferences are per user, not per trip: someone who does not want expense
// pings does not want them on any trip, and a per-trip matrix is more settings
// UI than this product can justify.
type Preferences struct {
	UserID      core.ID   `json:"-"            gorm:"primaryKey"`
	TripUpdates bool      `json:"trip_updates" gorm:"not null;default:true"`
	Decisions   bool      `json:"decisions"    gorm:"not null;default:true"`
	Reminders   bool      `json:"reminders"    gorm:"not null;default:true"`
	Checklist   bool      `json:"checklist"    gorm:"not null;default:true"`
	Expenses    bool      `json:"expenses"     gorm:"not null;default:false"`
	UpdatedAt   time.Time `json:"-"            gorm:"not null"`
}

func (Preferences) TableName() string { return "notification_preferences" }

// DefaultPreferences are what a user gets before touching settings: everything
// that needs a decision from them, nothing that is merely informational.
func DefaultPreferences() Preferences {
	return Preferences{TripUpdates: true, Decisions: true, Reminders: true, Checklist: true, Expenses: false}
}

// Allows reports whether this category may be delivered.
func (p Preferences) Allows(c Category) bool {
	switch c {
	case CategoryTripUpdates:
		return p.TripUpdates
	case CategoryDecisions:
		return p.Decisions
	case CategoryReminders:
		return p.Reminders
	case CategoryChecklist:
		return p.Checklist
	case CategoryExpenses:
		return p.Expenses
	}
	return false
}

// Notification is one message destined for one user.
type Notification struct {
	ID       core.ID      `json:"id"                gorm:"primaryKey"`
	TripID   core.ID      `json:"trip_id,omitempty" gorm:"index"`
	UserID   core.ID      `json:"user_id"           gorm:"not null;index"`
	Category Category     `json:"category"          gorm:"size:32;not null"`
	Title    string       `json:"title"             gorm:"size:200;not null;default:''"`
	Body     string       `json:"body"              gorm:"size:2000;not null"`
	Payload  core.JSONMap `json:"payload,omitempty"`
	// DedupeKey makes enqueueing idempotent. Empty means "always send", which
	// is why it is a pointer: SQL unique indexes ignore NULLs.
	DedupeKey   *string    `json:"-"            gorm:"size:200;uniqueIndex"`
	Status      string     `json:"-"            gorm:"size:16;not null;default:'pending';index:idx_notifications_due,priority:1"`
	ScheduledAt time.Time  `json:"scheduled_at" gorm:"not null;index:idx_notifications_due,priority:2"`
	SentAt      *time.Time `json:"-"`
	ClaimedAt   *time.Time `json:"-"`
	Attempts    int        `json:"-" gorm:"not null;default:0"`
	LastError   string     `json:"-" gorm:"size:500;not null;default:''"`
	CreatedAt   time.Time  `json:"created_at" gorm:"not null"`

	// ChatID is joined in when the worker claims the notification; it lives on
	// the user, not on the outbox row.
	ChatID int64 `json:"-" gorm:"-"`
}

func (Notification) TableName() string { return "notifications" }

// Enqueuer is the narrow interface domain modules depend on.
type Enqueuer interface {
	Enqueue(ctx context.Context, items ...Notification) error
}

type Service struct {
	db  *gorm.DB
	log *slog.Logger
}

func NewService(gdb *gorm.DB, log *slog.Logger) *Service { return &Service{db: gdb, log: log} }

// Enqueue stores notifications the user has not muted. Rows whose dedupe key
// already exists are dropped silently.
func (s *Service) Enqueue(ctx context.Context, items ...Notification) error {
	if len(items) == 0 {
		return nil
	}
	userIDs := make([]core.ID, 0, len(items))
	for _, n := range items {
		userIDs = append(userIDs, n.UserID)
	}
	prefs, err := s.PreferencesFor(ctx, userIDs)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, n := range items {
		if n.UserID.IsZero() || n.Body == "" {
			continue
		}
		if !n.Category.Valid() {
			return core.Invalid("unknown notification category %q", n.Category)
		}
		if !prefs[n.UserID].Allows(n.Category) {
			continue
		}
		n.ID = core.NewID()
		n.Status = StatusPending
		n.CreatedAt = now
		if n.ScheduledAt.IsZero() {
			n.ScheduledAt = now
		}
		n.ChatID = 0

		// DO NOTHING rather than letting the unique index raise: a reminder
		// that already exists is the normal outcome of the scheduler
		// re-evaluating a rule, not a problem. Catching the error after the
		// fact worked, but the driver had already logged a failed statement,
		// so every tick wrote an alarming warning about working as designed.
		//
		// A NULL dedupe_key never conflicts, so unkeyed notifications are
		// always inserted.
		err := s.db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&n).Error
		if err != nil {
			// Belt and braces: DO NOTHING should have covered it.
			if db.IsDuplicate(err) {
				continue
			}
			return core.Internal(fmt.Errorf("notify: enqueue: %w", err))
		}
	}
	return nil
}

// ClaimDue locks up to limit due notifications for delivery.
//
// Claiming is a status flip to "sending" rather than a row lock, because
// SELECT ... FOR UPDATE SKIP LOCKED does not exist on SQLite. A worker that
// dies mid-delivery leaves rows in "sending"; they become claimable again
// after staleClaim.
func (s *Service) ClaimDue(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 20
	}
	now := time.Now().UTC()
	var claimed []Notification

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidates []Notification
		err := tx.Where("(status = ?) OR (status = ? AND claimed_at < ?)",
			StatusPending, StatusSending, now.Add(-staleClaim)).
			Where("scheduled_at <= ?", now).
			Order("scheduled_at").
			Limit(limit).
			Find(&candidates).Error
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		ids := make([]string, 0, len(candidates))
		for _, n := range candidates {
			ids = append(ids, n.ID.String())
		}
		res := tx.Model(&Notification{}).
			Where("id IN ?", ids).
			Where("(status = ?) OR (status = ? AND claimed_at < ?)",
				StatusPending, StatusSending, now.Add(-staleClaim)).
			Updates(map[string]any{
				"status":     StatusSending,
				"claimed_at": now,
				"attempts":   gorm.Expr("attempts + 1"),
			})
		if res.Error != nil {
			return res.Error
		}
		return tx.Where("id IN ? AND status = ?", ids, StatusSending).Find(&claimed).Error
	})
	if err != nil {
		return nil, core.Internal(fmt.Errorf("notify: claim: %w", err))
	}
	if len(claimed) == 0 {
		return []Notification{}, nil
	}
	return s.attachChatIDs(ctx, claimed)
}

// attachChatIDs joins in where each recipient can be reached.
func (s *Service) attachChatIDs(ctx context.Context, items []Notification) ([]Notification, error) {
	userIDs := make([]string, 0, len(items))
	for _, n := range items {
		userIDs = append(userIDs, n.UserID.String())
	}
	type chatRow struct {
		ID     core.ID
		ChatID int64
	}
	var rows []chatRow
	if err := s.db.WithContext(ctx).Table("users").
		Select("id, chat_id").Where("id IN ?", userIDs).Scan(&rows).Error; err != nil {
		return nil, core.Internal(fmt.Errorf("notify: load chat ids: %w", err))
	}
	chats := make(map[core.ID]int64, len(rows))
	for _, r := range rows {
		chats[r.ID] = r.ChatID
	}
	for i := range items {
		items[i].ChatID = chats[items[i].UserID]
	}
	return items, nil
}

// MarkSent closes out a delivered notification.
func (s *Service) MarkSent(ctx context.Context, id core.ID) error {
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Model(&Notification{}).Where("id = ?", id).
		Updates(map[string]any{"status": StatusSent, "sent_at": now, "last_error": ""}).Error
	if err != nil {
		return core.Internal(fmt.Errorf("notify: mark sent: %w", err))
	}
	return nil
}

// MarkFailed records a delivery failure. After maxAttempts the notification is
// abandoned rather than retried forever; a stale reminder has no value.
func (s *Service) MarkFailed(ctx context.Context, id core.ID, cause string, maxAttempts int) error {
	var n Notification
	if err := s.db.WithContext(ctx).First(&n, "id = ?", id).Error; err != nil {
		if db.IsNotFound(err) {
			return nil
		}
		return core.Internal(fmt.Errorf("notify: mark failed: %w", err))
	}
	updates := map[string]any{"last_error": truncate(cause, 500)}
	if n.Attempts >= maxAttempts {
		updates["status"] = StatusFailed
	} else {
		updates["status"] = StatusPending
		updates["scheduled_at"] = time.Now().UTC().Add(time.Minute)
	}
	if err := s.db.WithContext(ctx).Model(&Notification{}).Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return core.Internal(fmt.Errorf("notify: mark failed: %w", err))
	}
	return nil
}

// MarkSkipped drops a notification we cannot deliver, e.g. the user has never
// opened a private chat with the bot so there is nowhere to send it.
func (s *Service) MarkSkipped(ctx context.Context, id core.ID, reason string) error {
	err := s.db.WithContext(ctx).Model(&Notification{}).Where("id = ?", id).
		Updates(map[string]any{"status": StatusSkipped, "last_error": truncate(reason, 500)}).Error
	if err != nil {
		return core.Internal(fmt.Errorf("notify: mark skipped: %w", err))
	}
	return nil
}

// Preferences returns one user's settings, falling back to the defaults.
func (s *Service) Preferences(ctx context.Context, userID core.ID) (Preferences, error) {
	all, err := s.PreferencesFor(ctx, []core.ID{userID})
	if err != nil {
		return Preferences{}, err
	}
	return all[userID], nil
}

// PreferencesFor bulk loads settings, defaulting for users who never changed them.
func (s *Service) PreferencesFor(ctx context.Context, userIDs []core.ID) (map[core.ID]Preferences, error) {
	out := map[core.ID]Preferences{}
	if len(userIDs) == 0 {
		return out, nil
	}
	for _, id := range userIDs {
		out[id] = DefaultPreferences()
	}
	var rows []Preferences
	err := s.db.WithContext(ctx).Find(&rows, "user_id IN ?", core.IDStrings(userIDs)).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("notify: load preferences: %w", err))
	}
	for _, p := range rows {
		out[p.UserID] = p
	}
	return out, nil
}

// SavePreferences replaces a user's settings.
func (s *Service) SavePreferences(ctx context.Context, userID core.ID, p Preferences) (Preferences, error) {
	p.UserID = userID
	p.UpdatedAt = time.Now().UTC()
	if err := s.db.WithContext(ctx).Save(&p).Error; err != nil {
		return Preferences{}, core.Internal(fmt.Errorf("notify: save preferences: %w", err))
	}
	return p, nil
}

// DedupeKey is a convenience for building the optional key field.
func DedupeKey(parts ...string) *string {
	if len(parts) == 0 {
		return nil
	}
	key := parts[0]
	for _, p := range parts[1:] {
		key += ":" + p
	}
	return &key
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// Nop discards notifications, for tests and for running without a bot token.
type Nop struct{}

func (Nop) Enqueue(context.Context, ...Notification) error { return nil }

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Preferences{}, &Notification{}} }
