package checklists

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

type Service struct {
	database *db.DB
	repo     *Repo
	trips    *trips.Service
	activity activity.Logger
}

func NewService(database *db.DB, tripSvc *trips.Service, act activity.Logger) *Service {
	return &Service{database: database, repo: NewRepo(database.DB), trips: tripSvc, activity: act}
}

func (s *Service) Repo() *Repo { return s.repo }

// ListInput creates a checklist. A personal list always belongs to the caller;
// nobody creates a private packing list on someone else's behalf.
type ListInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Scope       Scope    `json:"scope"`
	Items       []string `json:"items"`
}

// Lists returns the shared lists plus the caller's personal ones, each with
// its items and progress.
func (s *Service) Lists(ctx context.Context, access trips.Access) ([]Checklist, error) {
	lists, err := s.repo.ListsForMember(ctx, access.Trip.ID, access.Member.ID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ItemsByList(ctx, access.Trip.ID, access.Member.ID)
	if err != nil {
		return nil, err
	}
	for i := range lists {
		lists[i].Items = items[lists[i].ID]
		if lists[i].Items == nil {
			lists[i].Items = []Item{}
		}
		lists[i].Progress = ProgressOf(lists[i].Items)
	}
	return lists, nil
}

// Progress is the trip-wide count for the dashboard.
func (s *Service) Progress(ctx context.Context, access trips.Access) (Progress, error) {
	return s.repo.TripProgress(ctx, access.Trip.ID, access.Member.ID)
}

// CreateList adds a checklist. Shared lists are an organiser action; personal
// lists are not, because they only affect their owner.
func (s *Service) CreateList(ctx context.Context, access trips.Access, in ListInput) (Checklist, error) {
	if err := access.RequireWrite(); err != nil {
		return Checklist{}, err
	}
	if in.Scope == "" {
		in.Scope = ScopeShared
	}
	if in.Scope == ScopeShared && !access.IsManager() {
		return Checklist{}, core.Forbidden("only organisers can create shared checklists")
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)

	v := core.NewValidator()
	v.Required(in.Title, "title")
	v.Length(in.Title, "title", 1, 120)
	v.Length(in.Description, "description", 0, 1000)
	v.Check(in.Scope.Valid(), "scope", "must be shared or personal")
	v.Check(len(in.Items) <= 100, "items", "supports at most 100 items at once")
	if err := v.Err(); err != nil {
		return Checklist{}, err
	}

	list := Checklist{
		ID: core.NewID(), TripID: access.Trip.ID, Title: in.Title, Description: in.Description,
		Scope: in.Scope, CreatedBy: access.User.ID,
	}
	if in.Scope == ScopePersonal {
		list.OwnerMemberID = access.Member.ID
	}

	err := s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.InsertList(ctx, list)
		if err != nil {
			return err
		}
		list = created
		for _, title := range in.Items {
			title = strings.TrimSpace(title)
			if title == "" {
				continue
			}
			if _, err := repo.InsertItem(ctx, Item{
				ID: core.NewID(), ChecklistID: list.ID, Title: title,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Checklist{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindChecklistUpdated,
		Message: fmt.Sprintf("%s created the %q checklist", access.Member.DisplayName, list.Title),
	})
	return s.getList(ctx, access, list.ID)
}

// ListPatch renames a checklist.
type ListPatch struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

func (s *Service) UpdateList(ctx context.Context, access trips.Access, listID core.ID, in ListPatch) (Checklist, error) {
	list, err := s.loadList(ctx, access, listID)
	if err != nil {
		return Checklist{}, err
	}
	if err := s.requireEdit(access, list); err != nil {
		return Checklist{}, err
	}
	if in.Title != nil {
		list.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		list.Description = strings.TrimSpace(*in.Description)
	}
	v := core.NewValidator()
	v.Required(list.Title, "title")
	v.Length(list.Title, "title", 1, 120)
	v.Length(list.Description, "description", 0, 1000)
	if err := v.Err(); err != nil {
		return Checklist{}, err
	}
	if _, err := s.repo.UpdateList(ctx, list); err != nil {
		return Checklist{}, err
	}
	return s.getList(ctx, access, listID)
}

func (s *Service) DeleteList(ctx context.Context, access trips.Access, listID core.ID) error {
	list, err := s.loadList(ctx, access, listID)
	if err != nil {
		return err
	}
	if err := s.requireEdit(access, list); err != nil {
		return err
	}
	return s.repo.DeleteList(ctx, listID)
}

// ItemInput adds a line to a list.
type ItemInput struct {
	Title      string     `json:"title"`
	Notes      string     `json:"notes"`
	AssignedTo core.ID    `json:"assigned_to"`
	DueAt      *time.Time `json:"due_at"`
}

func (s *Service) AddItem(ctx context.Context, access trips.Access, listID core.ID, in ItemInput) (Item, error) {
	list, err := s.loadList(ctx, access, listID)
	if err != nil {
		return Item{}, err
	}
	if err := access.RequireWrite(); err != nil {
		return Item{}, err
	}
	// Anyone on the trip may add to a shared list: spotting a missing item is
	// not an organiser privilege. Only the owner touches a personal list.
	if list.Scope == ScopePersonal && list.OwnerMemberID != access.Member.ID && !access.IsManager() {
		return Item{}, core.Forbidden("that is somebody else's personal checklist")
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Notes = strings.TrimSpace(in.Notes)

	v := core.NewValidator()
	v.Required(in.Title, "title")
	v.Length(in.Title, "title", 1, 160)
	v.Length(in.Notes, "notes", 0, 500)
	if err := v.Err(); err != nil {
		return Item{}, err
	}
	if !in.AssignedTo.IsZero() {
		if err := s.checkAssignee(ctx, access, in.AssignedTo); err != nil {
			return Item{}, err
		}
	}
	return s.repo.InsertItem(ctx, Item{
		ID: core.NewID(), ChecklistID: listID, Title: in.Title, Notes: in.Notes,
		AssignedTo: in.AssignedTo, DueAt: core.UTCPtr(in.DueAt),
	})
}

// ItemPatch updates a line. Completing is separated from editing in the
// permission rules below, not in the payload.
type ItemPatch struct {
	Title         *string    `json:"title"`
	Notes         *string    `json:"notes"`
	Completed     *bool      `json:"completed"`
	AssignedTo    *core.ID   `json:"assigned_to"`
	ClearAssignee bool       `json:"clear_assignee"`
	DueAt         *time.Time `json:"due_at"`
	ClearDueAt    bool       `json:"clear_due_at"`
}

// UpdateItem applies a patch, enforcing the split between ticking a box and
// editing the list:
//
//   - anyone active may tick or untick a shared item that is unassigned or
//     assigned to them;
//   - organisers may tick anything and edit anything;
//   - a personal list is only its owner's business.
func (s *Service) UpdateItem(ctx context.Context, access trips.Access, itemID core.ID, in ItemPatch) (Item, error) {
	if err := access.RequireWrite(); err != nil {
		return Item{}, err
	}
	tripID, scope, owner, err := s.repo.ItemTrip(ctx, itemID)
	if err != nil {
		return Item{}, err
	}
	if tripID != access.Trip.ID {
		return Item{}, core.NotFound("checklist item")
	}
	item, err := s.repo.ItemByID(ctx, itemID)
	if err != nil {
		return Item{}, err
	}
	if scope == ScopePersonal && owner != access.Member.ID && !access.IsManager() {
		return Item{}, core.Forbidden("that is somebody else's personal checklist")
	}

	editing := in.Title != nil || in.Notes != nil || in.AssignedTo != nil ||
		in.ClearAssignee || in.DueAt != nil || in.ClearDueAt
	mine := scope == ScopePersonal || item.AssignedTo.IsZero() || item.AssignedTo == access.Member.ID
	if editing && scope == ScopeShared && !access.IsManager() {
		return Item{}, core.Forbidden("only organisers can edit shared checklist items")
	}
	if in.Completed != nil && !mine && !access.IsManager() {
		return Item{}, core.Forbidden("%s is responsible for that item", item.AssignedName)
	}

	if in.Title != nil {
		item.Title = strings.TrimSpace(*in.Title)
	}
	if in.Notes != nil {
		item.Notes = strings.TrimSpace(*in.Notes)
	}
	if in.ClearAssignee {
		item.AssignedTo = core.Nil
	} else if in.AssignedTo != nil {
		if err := s.checkAssignee(ctx, access, *in.AssignedTo); err != nil {
			return Item{}, err
		}
		item.AssignedTo = *in.AssignedTo
	}
	if in.ClearDueAt {
		item.DueAt = nil
	} else if in.DueAt != nil {
		item.DueAt = core.UTCPtr(in.DueAt)
	}
	completedNow := false
	if in.Completed != nil && *in.Completed != item.Completed {
		item.Completed = *in.Completed
		completedNow = item.Completed
		if item.Completed {
			now := time.Now().UTC()
			item.CompletedBy = access.Member.ID
			item.CompletedAt = &now
		} else {
			item.CompletedBy = core.Nil
			item.CompletedAt = nil
		}
	}

	v := core.NewValidator()
	v.Required(item.Title, "title")
	v.Length(item.Title, "title", 1, 160)
	v.Length(item.Notes, "notes", 0, 500)
	if err := v.Err(); err != nil {
		return Item{}, err
	}

	updated, err := s.repo.UpdateItem(ctx, item)
	if err != nil {
		return Item{}, err
	}
	if completedNow {
		s.activity.Log(ctx, activity.Entry{
			TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
			ActorName: access.Member.DisplayName, Kind: activity.KindChecklistItemDone,
			Message: fmt.Sprintf("%s completed %q", access.Member.DisplayName, updated.Title),
		})
	}
	return updated, nil
}

func (s *Service) DeleteItem(ctx context.Context, access trips.Access, itemID core.ID) error {
	if err := access.RequireWrite(); err != nil {
		return err
	}
	tripID, scope, owner, err := s.repo.ItemTrip(ctx, itemID)
	if err != nil {
		return err
	}
	if tripID != access.Trip.ID {
		return core.NotFound("checklist item")
	}
	switch scope {
	case ScopePersonal:
		if owner != access.Member.ID && !access.IsManager() {
			return core.Forbidden("that is somebody else's personal checklist")
		}
	default:
		if !access.IsManager() {
			return core.Forbidden("only organisers can remove shared checklist items")
		}
	}
	return s.repo.DeleteItem(ctx, itemID)
}

// ----------------------------------------------------------------- helpers --

func (s *Service) loadList(ctx context.Context, access trips.Access, listID core.ID) (Checklist, error) {
	list, err := s.repo.ListByID(ctx, listID)
	if err != nil {
		return Checklist{}, err
	}
	if list.TripID != access.Trip.ID {
		return Checklist{}, core.NotFound("checklist")
	}
	if list.Scope == ScopePersonal && list.OwnerMemberID != access.Member.ID && !access.IsManager() {
		return Checklist{}, core.NotFound("checklist")
	}
	return list, nil
}

func (s *Service) requireEdit(access trips.Access, list Checklist) error {
	if err := access.RequireWrite(); err != nil {
		return err
	}
	if list.Scope == ScopePersonal {
		if list.OwnerMemberID != access.Member.ID {
			return core.Forbidden("that is somebody else's personal checklist")
		}
		return nil
	}
	return access.Require(core.RoleAdmin)
}

func (s *Service) checkAssignee(ctx context.Context, access trips.Access, memberID core.ID) error {
	member, err := s.trips.MemberByID(ctx, access.Trip.ID, memberID)
	if err != nil {
		return err
	}
	if !member.Active() {
		return core.Invalid("items can only be assigned to active participants")
	}
	return nil
}

func (s *Service) getList(ctx context.Context, access trips.Access, listID core.ID) (Checklist, error) {
	lists, err := s.Lists(ctx, access)
	if err != nil {
		return Checklist{}, err
	}
	for _, l := range lists {
		if l.ID == listID {
			return l, nil
		}
	}
	return Checklist{}, core.NotFound("checklist")
}
