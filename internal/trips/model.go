// Package trips owns the trip itself and everything that decides who may touch
// it: membership and invites.
//
// Trip, membership and invites are one aggregate on purpose. Every other
// module authorizes its requests by asking this package for an Access, so
// keeping them together is what stops the rest of the codebase from having to
// reason about roles at all.
package trips

import (
	"strings"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Trip is the root object: everything else in the product belongs to one.
type Trip struct {
	ID          core.ID         `json:"id"          gorm:"primaryKey"`
	Title       string          `json:"title"       gorm:"size:120;not null"`
	Description string          `json:"description" gorm:"size:2000;not null;default:''"`
	StartDate   core.Date       `json:"start_date"  gorm:"not null;index"`
	EndDate     core.Date       `json:"end_date"    gorm:"not null"`
	Timezone    string          `json:"timezone"    gorm:"size:64;not null;default:'UTC'"`
	Currency    string          `json:"currency"    gorm:"size:3;not null;default:'EUR'"`
	OwnerID     core.ID         `json:"owner_id"    gorm:"not null;index"`
	Status      core.TripStatus `json:"status"      gorm:"size:16;not null;default:'planning';index"`
	CreatedAt   time.Time       `json:"created_at"  gorm:"not null"`
	UpdatedAt   time.Time       `json:"updated_at"  gorm:"not null"`
}

func (Trip) TableName() string { return "trips" }

// Location resolves the trip timezone, which is what all display conversion
// uses. Stored instants are always UTC.
func (t Trip) Location() *time.Location { return core.LoadLocation(t.Timezone) }

// Nights is how many overnight stays the dates imply; zero means a day trip.
func (t Trip) Nights() int { return t.StartDate.Nights(t.EndDate) }

// Member is a participant of one trip.
type Member struct {
	ID     core.ID `json:"id"      gorm:"primaryKey"`
	TripID core.ID `json:"trip_id" gorm:"not null;index;uniqueIndex:idx_trip_members_user,priority:1"`
	UserID core.ID `json:"user_id" gorm:"not null;index;uniqueIndex:idx_trip_members_user,priority:2"`
	// DisplayName starts as the Telegram name but is editable per trip: the
	// group knows each other by nicknames, not by Telegram profiles.
	DisplayName string            `json:"display_name" gorm:"size:60;not null"`
	Role        core.Role         `json:"role"         gorm:"size:16;not null;default:'member'"`
	Status      core.MemberStatus `json:"status"       gorm:"size:16;not null;default:'active'"`
	JoinedAt    *time.Time        `json:"joined_at,omitempty"`
	CreatedAt   time.Time         `json:"created_at" gorm:"not null"`
	UpdatedAt   time.Time         `json:"-"          gorm:"not null"`

	// Telegram profile, joined in for rendering. Not stored on this table.
	TelegramID int64  `json:"telegram_id,omitempty" gorm:"-"`
	Username   string `json:"username,omitempty"    gorm:"-"`
	PhotoURL   string `json:"photo_url,omitempty"   gorm:"-"`
}

func (Member) TableName() string { return "trip_members" }

// Initials is the two letter fallback avatar used all over the UI.
func (m Member) Initials() string {
	fields := strings.Fields(strings.TrimSpace(m.DisplayName))
	switch len(fields) {
	case 0:
		return "??"
	case 1:
		r := []rune(fields[0])
		if len(r) == 1 {
			return strings.ToUpper(string(r[0]))
		}
		return strings.ToUpper(string(r[0:2]))
	default:
		return strings.ToUpper(string([]rune(fields[0])[0:1]) + string([]rune(fields[1])[0:1]))
	}
}

// Active reports whether the member counts towards the group.
func (m Member) Active() bool { return m.Status == core.MemberActive }

// Invite is a signed-by-randomness link that lets someone join a trip.
// The token is what appears in the deep link, never the trip id.
type Invite struct {
	ID        core.ID   `json:"id"         gorm:"primaryKey"`
	TripID    core.ID   `json:"trip_id"    gorm:"not null;index"`
	Token     string    `json:"token"      gorm:"size:64;not null;uniqueIndex"`
	CreatedBy core.ID   `json:"created_by" gorm:"not null"`
	Role      core.Role `json:"role"       gorm:"size:16;not null;default:'member'"`
	Label     string    `json:"label,omitempty" gorm:"size:60;not null;default:''"`
	// MaxUses of zero means unlimited, the right default for a link dropped
	// into a group chat.
	MaxUses   int        `json:"max_uses" gorm:"not null;default:0"`
	Uses      int        `json:"uses"     gorm:"not null;default:0"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at" gorm:"not null"`

	// URL is filled in by the service; it depends on the bot username.
	URL string `json:"url,omitempty" gorm:"-"`
}

func (Invite) TableName() string { return "trip_invites" }

// Usable reports whether the invite can still be redeemed at time now.
func (i Invite) Usable(now time.Time) bool {
	switch {
	case i.RevokedAt != nil:
		return false
	case i.ExpiresAt != nil && now.After(*i.ExpiresAt):
		return false
	case i.MaxUses > 0 && i.Uses >= i.MaxUses:
		return false
	default:
		return true
	}
}

// MemberCounts summarises the group for the dashboard.
type MemberCounts struct {
	Total   int `json:"total"`
	Active  int `json:"active"`
	Invited int `json:"invited"`
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Trip{}, &Member{}, &Invite{}} }
