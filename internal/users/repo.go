package users

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

// Upsert creates the user on first contact and refreshes their Telegram
// profile afterwards.
//
// It is a read-then-write rather than a dialect-specific upsert because the
// chat id must only be overwritten when the caller actually knows one: a Mini
// App request carries no chat, and clobbering it there would leave the user
// unreachable by notifications.
func (r *Repo) Upsert(ctx context.Context, identity Identity) (User, error) {
	var user User
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("telegram_id = ?", identity.TelegramID).First(&user).Error
		switch {
		case db.IsNotFound(err):
			now := time.Now().UTC()
			user = User{
				ID:           core.NewID(),
				TelegramID:   identity.TelegramID,
				Username:     identity.Username,
				FirstName:    identity.FirstName,
				LastName:     identity.LastName,
				LanguageCode: identity.LanguageCode,
				PhotoURL:     identity.PhotoURL,
				IsPremium:    identity.IsPremium,
				ChatID:       identity.ChatID,
				CreatedAt:    now,
				UpdatedAt:    now,
			}
			return tx.Create(&user).Error
		case err != nil:
			return err
		}

		user.Username = identity.Username
		user.FirstName = identity.FirstName
		user.LastName = identity.LastName
		user.LanguageCode = identity.LanguageCode
		user.PhotoURL = identity.PhotoURL
		user.IsPremium = identity.IsPremium
		if identity.ChatID != 0 {
			user.ChatID = identity.ChatID
		}
		user.UpdatedAt = time.Now().UTC()
		return tx.Save(&user).Error
	})
	if err != nil {
		// Two first-contact requests can race; the loser reads the winner's row.
		if db.IsDuplicate(err) {
			return r.ByTelegramID(ctx, identity.TelegramID)
		}
		return User{}, core.Internal(fmt.Errorf("users: upsert: %w", err))
	}
	return user, nil
}

func (r *Repo) ByID(ctx context.Context, id core.ID) (User, error) {
	var user User
	err := r.db.WithContext(ctx).First(&user, "id = ?", id).Error
	if db.IsNotFound(err) {
		return User{}, core.NotFound("user")
	}
	if err != nil {
		return User{}, core.Internal(fmt.Errorf("users: by id: %w", err))
	}
	return user, nil
}

func (r *Repo) ByTelegramID(ctx context.Context, telegramID int64) (User, error) {
	var user User
	err := r.db.WithContext(ctx).First(&user, "telegram_id = ?", telegramID).Error
	if db.IsNotFound(err) {
		return User{}, core.NotFound("user")
	}
	if err != nil {
		return User{}, core.Internal(fmt.Errorf("users: by telegram id: %w", err))
	}
	return user, nil
}

// ByIDs loads many users at once, keyed by id, for list rendering.
func (r *Repo) ByIDs(ctx context.Context, ids []core.ID) (map[core.ID]User, error) {
	out := map[core.ID]User{}
	if len(ids) == 0 {
		return out, nil
	}
	var list []User
	if err := r.db.WithContext(ctx).Find(&list, "id IN ?", core.IDStrings(ids)).Error; err != nil {
		return nil, core.Internal(fmt.Errorf("users: by ids: %w", err))
	}
	for _, u := range list {
		out[u.ID] = u
	}
	return out, nil
}

// SetChatID remembers where the bot can reach this user.
func (r *Repo) SetChatID(ctx context.Context, userID core.ID, chatID int64) error {
	err := r.db.WithContext(ctx).Model(&User{}).Where("id = ?", userID).
		Updates(map[string]any{"chat_id": chatID, "updated_at": time.Now().UTC()}).Error
	if err != nil {
		return core.Internal(fmt.Errorf("users: set chat id: %w", err))
	}
	return nil
}
