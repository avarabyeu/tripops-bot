package checklists

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

// ------------------------------------------------------------------- lists --

// listRow is a checklist with its owner's name joined in.
type listRow struct {
	Checklist
	JoinedOwnerName string
}

func (row listRow) checklist() Checklist {
	c := row.Checklist
	c.OwnerName = row.JoinedOwnerName
	return c
}

func (r *Repo) listQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("checklists AS c").
		Select("c.*, coalesce(o.display_name, '') AS joined_owner_name").
		Joins("LEFT JOIN trip_members o ON o.id = c.owner_member_id")
}

func (r *Repo) InsertList(ctx context.Context, c Checklist) (Checklist, error) {
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	c.Position = r.nextPosition(ctx, "checklists", "trip_id", c.TripID)
	if err := r.db.WithContext(ctx).Create(&c).Error; err != nil {
		return Checklist{}, core.Internal(fmt.Errorf("checklists: insert list: %w", err))
	}
	return r.ListByID(ctx, c.ID)
}

func (r *Repo) ListByID(ctx context.Context, id core.ID) (Checklist, error) {
	var row listRow
	if err := r.listQuery(ctx).Where("c.id = ?", id).Limit(1).Scan(&row).Error; err != nil {
		return Checklist{}, core.Internal(fmt.Errorf("checklists: by id: %w", err))
	}
	if row.ID.IsZero() {
		return Checklist{}, core.NotFound("checklist")
	}
	return row.checklist(), nil
}

func (r *Repo) UpdateList(ctx context.Context, c Checklist) (Checklist, error) {
	res := r.db.WithContext(ctx).Model(&Checklist{}).Where("id = ?", c.ID).
		Updates(map[string]any{
			"title":       c.Title,
			"description": c.Description,
			"updated_at":  time.Now().UTC(),
		})
	if res.Error != nil {
		return Checklist{}, core.Internal(fmt.Errorf("checklists: update list: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Checklist{}, core.NotFound("checklist")
	}
	return r.ListByID(ctx, c.ID)
}

func (r *Repo) DeleteList(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Delete(&Checklist{}, "id = ?", id)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("checklists: delete list: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("checklist")
	}
	return nil
}

// ListsForMember returns the shared lists plus the caller's own personal ones.
// Somebody else's personal packing list is none of your business.
func (r *Repo) ListsForMember(ctx context.Context, tripID, memberID core.ID) ([]Checklist, error) {
	var rows []listRow
	err := r.listQuery(ctx).
		Where("c.trip_id = ?", tripID).
		Where("c.scope = ? OR c.owner_member_id = ?", string(ScopeShared), memberID).
		Order("c.scope DESC, c.position, c.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("checklists: list: %w", err))
	}
	out := make([]Checklist, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.checklist())
	}
	return out, nil
}

// ------------------------------------------------------------------- items --

// itemRow is an item with the assignee and completer names joined in.
type itemRow struct {
	Item
	JoinedAssignedName    string
	JoinedCompletedByName string
}

func (row itemRow) item() Item {
	i := row.Item
	i.AssignedName = row.JoinedAssignedName
	i.CompletedByName = row.JoinedCompletedByName
	return i
}

func (r *Repo) itemQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("checklist_items AS i").
		Select(`i.*, coalesce(am.display_name, '') AS joined_assigned_name,
			coalesce(cb.display_name, '') AS joined_completed_by_name`).
		Joins("LEFT JOIN trip_members am ON am.id = i.assigned_to").
		Joins("LEFT JOIN trip_members cb ON cb.id = i.completed_by")
}

func (r *Repo) InsertItem(ctx context.Context, item Item) (Item, error) {
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	item.Position = r.nextPosition(ctx, "checklist_items", "checklist_id", item.ChecklistID)
	if err := r.db.WithContext(ctx).Create(&item).Error; err != nil {
		if db.IsForeignKeyViolation(err) {
			return Item{}, core.NotFound("checklist")
		}
		return Item{}, core.Internal(fmt.Errorf("checklists: insert item: %w", err))
	}
	return r.ItemByID(ctx, item.ID)
}

func (r *Repo) ItemByID(ctx context.Context, id core.ID) (Item, error) {
	var row itemRow
	if err := r.itemQuery(ctx).Where("i.id = ?", id).Limit(1).Scan(&row).Error; err != nil {
		return Item{}, core.Internal(fmt.Errorf("checklists: item by id: %w", err))
	}
	if row.ID.IsZero() {
		return Item{}, core.NotFound("checklist item")
	}
	return row.item(), nil
}

// ItemTrip returns the trip and scope an item belongs to, so a handler can
// authorize a bare item id without a trip in the path.
func (r *Repo) ItemTrip(ctx context.Context, itemID core.ID) (tripID core.ID, scope Scope, owner core.ID, err error) {
	var row struct {
		TripID        core.ID
		Scope         Scope
		OwnerMemberID core.ID
	}
	queryErr := r.db.WithContext(ctx).
		Table("checklist_items AS i").
		Select("c.trip_id, c.scope, c.owner_member_id").
		Joins("JOIN checklists c ON c.id = i.checklist_id").
		Where("i.id = ?", itemID).
		Limit(1).Scan(&row).Error
	if queryErr != nil {
		return core.Nil, "", core.Nil, core.Internal(fmt.Errorf("checklists: item trip: %w", queryErr))
	}
	if row.TripID.IsZero() {
		return core.Nil, "", core.Nil, core.NotFound("checklist item")
	}
	return row.TripID, row.Scope, row.OwnerMemberID, nil
}

func (r *Repo) UpdateItem(ctx context.Context, item Item) (Item, error) {
	updates := map[string]any{
		"title":       item.Title,
		"notes":       item.Notes,
		"completed":   item.Completed,
		"assigned_to": item.AssignedTo,
		"due_at":      item.DueAt,
		"updated_at":  time.Now().UTC(),
	}
	if item.Completed {
		updates["completed_by"] = item.CompletedBy
		completedAt := time.Now().UTC()
		if item.CompletedAt != nil {
			completedAt = *item.CompletedAt
		}
		updates["completed_at"] = completedAt
	} else {
		updates["completed_by"] = core.Nil
		updates["completed_at"] = nil
	}

	res := r.db.WithContext(ctx).Model(&Item{}).Where("id = ?", item.ID).Updates(updates)
	if res.Error != nil {
		return Item{}, core.Internal(fmt.Errorf("checklists: update item: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Item{}, core.NotFound("checklist item")
	}
	return r.ItemByID(ctx, item.ID)
}

func (r *Repo) DeleteItem(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Delete(&Item{}, "id = ?", id)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("checklists: delete item: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("checklist item")
	}
	return nil
}

// ItemsByList loads the items of every list visible to a member.
func (r *Repo) ItemsByList(ctx context.Context, tripID, memberID core.ID) (map[core.ID][]Item, error) {
	var rows []itemRow
	err := r.itemQuery(ctx).
		Joins("JOIN checklists c ON c.id = i.checklist_id").
		Where("c.trip_id = ?", tripID).
		Where("c.scope = ? OR c.owner_member_id = ?", string(ScopeShared), memberID).
		Order("i.position, i.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("checklists: items: %w", err))
	}
	out := map[core.ID][]Item{}
	for _, row := range rows {
		item := row.item()
		out[item.ChecklistID] = append(out[item.ChecklistID], item)
	}
	return out, nil
}

// TripProgress counts every shared item plus the member's own personal items,
// which is the number the dashboard shows that member.
func (r *Repo) TripProgress(ctx context.Context, tripID, memberID core.ID) (Progress, error) {
	var p Progress
	err := r.db.WithContext(ctx).
		Table("checklist_items AS i").
		Select(`count(CASE WHEN i.completed THEN 1 END) AS completed, count(*) AS total`).
		Joins("JOIN checklists c ON c.id = i.checklist_id").
		Where("c.trip_id = ?", tripID).
		Where("c.scope = ? OR c.owner_member_id = ?", string(ScopeShared), memberID).
		Scan(&p).Error
	if err != nil {
		return Progress{}, core.Internal(fmt.Errorf("checklists: progress: %w", err))
	}
	return p, nil
}

// Assignment is an outstanding assigned item, used by reminders and the
// attention engine.
type Assignment struct {
	ItemID      core.ID
	Title       string
	MemberID    core.ID
	UserID      core.ID
	DisplayName string
	DueAt       *time.Time
}

// OpenAssignments lists incomplete items that have an assignee.
func (r *Repo) OpenAssignments(ctx context.Context, tripID core.ID) ([]Assignment, error) {
	out := []Assignment{}
	err := r.db.WithContext(ctx).
		Table("checklist_items AS i").
		Select(`i.id AS item_id, i.title AS title, m.id AS member_id,
			m.user_id AS user_id, m.display_name AS display_name, i.due_at AS due_at`).
		Joins("JOIN checklists c ON c.id = i.checklist_id").
		Joins("JOIN trip_members m ON m.id = i.assigned_to").
		Where("c.trip_id = ? AND i.completed = ? AND m.status = ?", tripID, false, "active").
		Order("i.due_at, i.position").
		Scan(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("checklists: open assignments: %w", err))
	}
	return out, nil
}

// nextPosition appends new rows at the end of their list. Ordering is explicit
// rather than by creation time so a future drag-to-reorder has somewhere to go.
func (r *Repo) nextPosition(ctx context.Context, table, column string, parent core.ID) int {
	var result struct{ MaxPosition *int }
	err := r.db.WithContext(ctx).Table(table).
		Select("max(position) AS max_position").
		Where(column+" = ?", parent).
		Scan(&result).Error
	if err != nil || result.MaxPosition == nil {
		return 0
	}
	return *result.MaxPosition + 1
}
