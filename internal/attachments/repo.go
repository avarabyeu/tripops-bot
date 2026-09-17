package attachments

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// repo is the data access for attachments. It is unexported because the module
// is small enough that the service is the only caller.
type repo struct{ db *gorm.DB }

func (r repo) list(ctx context.Context, tripID core.ID, ownerType OwnerType, ownerID core.ID) ([]Attachment, error) {
	out := []Attachment{}
	err := r.db.WithContext(ctx).
		Where("trip_id = ? AND owner_type = ? AND owner_id = ?", tripID, string(ownerType), ownerID).
		Order("created_at").
		Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("attachments: list: %w", err))
	}
	return out, nil
}

// ownerExists checks that the object an attachment is being hung off exists
// and belongs to this trip, so an attachment can never be smuggled onto
// somebody else's expense.
func (r repo) ownerExists(ctx context.Context, tripID core.ID, ownerType OwnerType, ownerID core.ID) (bool, error) {
	var count int64
	query := r.db.WithContext(ctx)

	switch ownerType {
	case OwnerChecklistItem:
		// The trip is one join away for a checklist item.
		query = query.Table("checklist_items AS i").
			Joins("JOIN checklists c ON c.id = i.checklist_id").
			Where("i.id = ? AND c.trip_id = ?", ownerID, tripID)
	case OwnerTrip:
		query = query.Table("trips").Where("id = ? AND id = ?", ownerID, tripID)
	default:
		table := ownerType.table()
		if table == "" {
			return false, core.Invalid("unknown owner_type")
		}
		query = query.Table(table).Where("id = ? AND trip_id = ?", ownerID, tripID)
	}

	if err := query.Count(&count).Error; err != nil {
		return false, core.Internal(fmt.Errorf("attachments: owner check: %w", err))
	}
	return count > 0, nil
}
