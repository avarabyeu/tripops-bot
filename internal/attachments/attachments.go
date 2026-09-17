// Package attachments links files and links to trip objects.
//
// For the MVP the bytes are never ours: a Telegram photo or document stays in
// Telegram and we keep the file_id it hands us, and an external booking page
// is just a URL. That keeps the service stateless with respect to storage and
// is the reason there is no upload endpoint.
package attachments

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

// OwnerType names the object an attachment hangs off.
type OwnerType string

const (
	OwnerTrip          OwnerType = "trip"
	OwnerEvent         OwnerType = "event"
	OwnerExpense       OwnerType = "expense"
	OwnerAccommodation OwnerType = "accommodation"
	OwnerChecklistItem OwnerType = "checklist_item"
)

func (o OwnerType) Valid() bool {
	switch o {
	case OwnerTrip, OwnerEvent, OwnerExpense, OwnerAccommodation, OwnerChecklistItem:
		return true
	}
	return false
}

// table returns the table an owner id must exist in. Checklist items and the
// trip itself are special-cased by the repository.
func (o OwnerType) table() string {
	switch o {
	case OwnerTrip:
		return "trips"
	case OwnerEvent:
		return "events"
	case OwnerExpense:
		return "expenses"
	case OwnerAccommodation:
		return "accommodations"
	case OwnerChecklistItem:
		return "checklist_items"
	default:
		return ""
	}
}

// Kind is where the payload lives.
type Kind string

const (
	KindTelegramFile Kind = "telegram_file"
	KindURL          Kind = "url"
)

// Attachment is one file or link.
type Attachment struct {
	ID     core.ID `json:"id"      gorm:"primaryKey"`
	TripID core.ID `json:"trip_id" gorm:"not null;index"`
	// The owner is polymorphic, kept as (type, id) rather than one nullable
	// foreign key per table: attachments are read by owner, never joined
	// across owners.
	OwnerType OwnerType `json:"owner_type" gorm:"size:24;not null;index:idx_attachments_owner,priority:1"`
	OwnerID   core.ID   `json:"owner_id"   gorm:"not null;index:idx_attachments_owner,priority:2"`
	Kind      Kind      `json:"kind"       gorm:"size:16;not null"`

	FileID       string `json:"file_id,omitempty"        gorm:"size:256;not null;default:''"`
	FileUniqueID string `json:"file_unique_id,omitempty" gorm:"size:128;not null;default:''"`
	URL          string `json:"url,omitempty"            gorm:"size:1024;not null;default:''"`
	FileName     string `json:"file_name,omitempty"      gorm:"size:200;not null;default:''"`
	MimeType     string `json:"mime_type,omitempty"      gorm:"size:128;not null;default:''"`
	SizeBytes    int64  `json:"size_bytes,omitempty"     gorm:"not null;default:0"`
	Caption      string `json:"caption,omitempty"        gorm:"size:300;not null;default:''"`

	UploadedBy core.ID   `json:"uploaded_by" gorm:"not null"`
	CreatedAt  time.Time `json:"created_at"  gorm:"not null"`
}

func (Attachment) TableName() string { return "attachments" }

// Input adds an attachment. Exactly one of FileID or URL must be set.
type Input struct {
	OwnerType    OwnerType `json:"owner_type"`
	OwnerID      core.ID   `json:"owner_id"`
	FileID       string    `json:"file_id"`
	FileUniqueID string    `json:"file_unique_id"`
	URL          string    `json:"url"`
	FileName     string    `json:"file_name"`
	MimeType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	Caption      string    `json:"caption"`
}

type Service struct {
	repo repo
	db   *gorm.DB
}

func NewService(gdb *gorm.DB) *Service { return &Service{repo: repo{db: gdb}, db: gdb} }

// List returns the attachments of one object.
func (s *Service) List(ctx context.Context, access trips.Access, ownerType OwnerType, ownerID core.ID) ([]Attachment, error) {
	if !ownerType.Valid() {
		return nil, core.Invalid("unknown owner_type")
	}
	return s.repo.list(ctx, access.Trip.ID, ownerType, ownerID)
}

// Add attaches a Telegram file or a link to an object of this trip.
func (s *Service) Add(ctx context.Context, access trips.Access, in Input) (Attachment, error) {
	if err := access.RequireWrite(); err != nil {
		return Attachment{}, err
	}
	in.URL = strings.TrimSpace(in.URL)
	in.FileID = strings.TrimSpace(in.FileID)
	in.Caption = strings.TrimSpace(in.Caption)

	v := core.NewValidator()
	v.Check(in.OwnerType.Valid(), "owner_type", "is not a supported object")
	v.Check(!in.OwnerID.IsZero(), "owner_id", "is required")
	v.Check((in.FileID == "") != (in.URL == ""), "url", "provide either a Telegram file id or a URL")
	v.Length(in.Caption, "caption", 0, 300)
	v.Length(in.FileName, "file_name", 0, 200)
	if in.URL != "" {
		parsed, err := url.Parse(in.URL)
		v.Check(err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "",
			"url", "must be an http(s) link")
	}
	if err := v.Err(); err != nil {
		return Attachment{}, err
	}
	if err := s.checkOwner(ctx, access.Trip.ID, in.OwnerType, in.OwnerID); err != nil {
		return Attachment{}, err
	}

	kind := KindURL
	if in.FileID != "" {
		kind = KindTelegramFile
	}
	a := Attachment{
		ID: core.NewID(), TripID: access.Trip.ID, OwnerType: in.OwnerType, OwnerID: in.OwnerID,
		Kind: kind, FileID: in.FileID, FileUniqueID: in.FileUniqueID, URL: in.URL,
		FileName: in.FileName, MimeType: in.MimeType, SizeBytes: in.SizeBytes,
		Caption: in.Caption, UploadedBy: access.User.ID, CreatedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&a).Error; err != nil {
		return Attachment{}, core.Internal(fmt.Errorf("attachments: insert: %w", err))
	}
	return a, nil
}

// Delete removes an attachment. The uploader or an organiser may do it.
func (s *Service) Delete(ctx context.Context, access trips.Access, id core.ID) error {
	if err := access.RequireWrite(); err != nil {
		return err
	}
	var existing Attachment
	err := s.db.WithContext(ctx).First(&existing, "id = ? AND trip_id = ?", id, access.Trip.ID).Error
	if db.IsNotFound(err) {
		return core.NotFound("attachment")
	}
	if err != nil {
		return core.Internal(fmt.Errorf("attachments: load: %w", err))
	}
	if existing.UploadedBy != access.User.ID && !access.IsManager() {
		return core.Forbidden("only the person who attached this or an organiser can remove it")
	}
	if err := s.db.WithContext(ctx).Delete(&Attachment{}, "id = ?", id).Error; err != nil {
		return core.Internal(fmt.Errorf("attachments: delete: %w", err))
	}
	return nil
}

// checkOwner makes sure the object exists and belongs to this trip.
func (s *Service) checkOwner(ctx context.Context, tripID core.ID, ownerType OwnerType, ownerID core.ID) error {
	exists, err := s.repo.ownerExists(ctx, tripID, ownerType, ownerID)
	if err != nil {
		return err
	}
	if !exists {
		return core.NotFound(string(ownerType))
	}
	return nil
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Attachment{}} }
