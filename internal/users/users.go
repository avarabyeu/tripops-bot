// Package users maps Telegram identities onto internal user records. It is the
// only place that knows a Telegram id at all; every other module works with
// core.ID user ids.
package users

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// User is an account, always created implicitly from a Telegram identity.
// There are no passwords and no email in this product.
type User struct {
	ID           core.ID `json:"id"           gorm:"primaryKey"`
	TelegramID   int64   `json:"telegram_id"  gorm:"uniqueIndex;not null"`
	Username     string  `json:"username,omitempty"      gorm:"size:64;not null;default:''"`
	FirstName    string  `json:"first_name"              gorm:"size:128;not null;default:''"`
	LastName     string  `json:"last_name,omitempty"     gorm:"size:128;not null;default:''"`
	LanguageCode string  `json:"language_code,omitempty" gorm:"size:16;not null;default:''"`
	PhotoURL     string  `json:"photo_url,omitempty"     gorm:"size:512;not null;default:''"`
	IsPremium    bool    `json:"is_premium,omitempty"    gorm:"not null;default:false"`
	// ChatID is the private chat the bot can reach this user in. Zero until
	// they have talked to the bot at least once, which is why notifications
	// can be skipped rather than failed.
	ChatID    int64     `json:"-"          gorm:"index"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`
}

// TableName pins the table name so a rename of the Go type cannot silently
// migrate data into a new table.
func (User) TableName() string { return "users" }

// DisplayName is the short name used across the UI: "Andrei V." style is left
// to the client; here we simply prefer the first name, then the username.
func (u User) DisplayName() string {
	switch {
	case strings.TrimSpace(u.FirstName) != "":
		name := strings.TrimSpace(u.FirstName)
		if ln := strings.TrimSpace(u.LastName); ln != "" {
			name += " " + ln
		}
		return name
	case u.Username != "":
		return "@" + u.Username
	default:
		return "Traveller"
	}
}

// Identity is what an authentication adapter knows about a Telegram user.
type Identity struct {
	TelegramID   int64
	Username     string
	FirstName    string
	LastName     string
	LanguageCode string
	PhotoURL     string
	IsPremium    bool
	// ChatID is set by the bot adapter, which is the only context where the
	// private chat id is known. Zero means "leave whatever is stored".
	ChatID int64
}

// Service resolves identities into users.
type Service struct {
	repo *Repo
}

func NewService(db *gorm.DB) *Service { return &Service{repo: NewRepo(db)} }

// EnsureUser creates the user on first contact and refreshes the Telegram
// profile fields on every subsequent one. Telegram is the source of truth for
// names and avatars, so there is nothing to merge.
func (s *Service) EnsureUser(ctx context.Context, id Identity) (User, error) {
	if id.TelegramID == 0 {
		return User{}, core.Invalid("telegram id is required")
	}
	return s.repo.Upsert(ctx, id)
}

// Get returns a user by internal id.
func (s *Service) Get(ctx context.Context, id core.ID) (User, error) {
	return s.repo.ByID(ctx, id)
}

// GetByTelegramID returns a user by their Telegram id.
func (s *Service) GetByTelegramID(ctx context.Context, telegramID int64) (User, error) {
	return s.repo.ByTelegramID(ctx, telegramID)
}

// ByIDs loads many users at once, keyed by id, for list rendering.
func (s *Service) ByIDs(ctx context.Context, ids []core.ID) (map[core.ID]User, error) {
	return s.repo.ByIDs(ctx, ids)
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&User{}} }
