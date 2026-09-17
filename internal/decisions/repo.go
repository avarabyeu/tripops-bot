package decisions

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

func (r *Repo) Insert(ctx context.Context, d Decision) (Decision, error) {
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&d).Error; err != nil {
		return Decision{}, core.Internal(fmt.Errorf("decisions: insert: %w", err))
	}
	return d, nil
}

func (r *Repo) InsertOptions(ctx context.Context, decisionID core.ID, options []Option) error {
	if len(options) == 0 {
		return nil
	}
	for i := range options {
		options[i].DecisionID = decisionID
		options[i].Position = i
	}
	if err := r.db.WithContext(ctx).Create(&options).Error; err != nil {
		return core.Internal(fmt.Errorf("decisions: insert options: %w", err))
	}
	return nil
}

func (r *Repo) ByID(ctx context.Context, id core.ID) (Decision, error) {
	var d Decision
	err := r.db.WithContext(ctx).First(&d, "id = ?", id).Error
	if db.IsNotFound(err) {
		return Decision{}, core.NotFound("decision")
	}
	if err != nil {
		return Decision{}, core.Internal(fmt.Errorf("decisions: by id: %w", err))
	}
	return d, nil
}

// ListByTrip returns open decisions first, then the rest newest first.
func (r *Repo) ListByTrip(ctx context.Context, tripID core.ID) ([]Decision, error) {
	out := []Decision{}
	err := r.db.WithContext(ctx).Where("trip_id = ?", tripID).
		Order("CASE WHEN status = 'open' THEN 0 ELSE 1 END, created_at DESC").
		Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("decisions: list: %w", err))
	}
	return out, nil
}

// Options loads the ballots of every decision of a trip, keyed by decision id.
func (r *Repo) Options(ctx context.Context, tripID core.ID) (map[core.ID][]Option, error) {
	var rows []Option
	err := r.db.WithContext(ctx).
		Table("decision_options AS o").
		Select("o.*").
		Joins("JOIN decisions d ON d.id = o.decision_id").
		Where("d.trip_id = ?", tripID).
		Order("o.decision_id, o.position").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("decisions: options: %w", err))
	}
	out := map[core.ID][]Option{}
	for _, o := range rows {
		o.Voters = []Voter{}
		out[o.DecisionID] = append(out[o.DecisionID], o)
	}
	return out, nil
}

// Vote is a single cast ballot, joined with the voter's name.
type Vote struct {
	DecisionID  core.ID
	OptionID    core.ID
	MemberID    core.ID
	DisplayName string
}

// Votes loads every vote on a trip's decisions.
func (r *Repo) Votes(ctx context.Context, tripID core.ID) ([]Vote, error) {
	out := []Vote{}
	err := r.db.WithContext(ctx).
		Table("decision_votes AS v").
		Select("v.decision_id, v.option_id, v.member_id, m.display_name").
		Joins("JOIN decisions d ON d.id = v.decision_id").
		Joins("JOIN trip_members m ON m.id = v.member_id").
		Where("d.trip_id = ?", tripID).
		Order("v.created_at").
		Scan(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("decisions: votes: %w", err))
	}
	return out, nil
}

// CastVote records or replaces a member's vote. Changing your mind while
// voting is open is allowed; the primary key guarantees it stays one vote.
func (r *Repo) CastVote(ctx context.Context, decisionID, memberID, optionID core.ID) error {
	now := time.Now().UTC()
	row := voteRow{
		DecisionID: decisionID, MemberID: memberID, OptionID: optionID,
		CreatedAt: now, UpdatedAt: now,
	}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "decision_id"}, {Name: "member_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"option_id", "updated_at"}),
	}).Create(&row).Error
	if db.IsForeignKeyViolation(err) {
		return core.NotFound("decision option")
	}
	if err != nil {
		return core.Internal(fmt.Errorf("decisions: cast vote: %w", err))
	}
	return nil
}

// OptionBelongsTo guards against voting for an option of another decision.
func (r *Repo) OptionBelongsTo(ctx context.Context, decisionID, optionID core.ID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Option{}).
		Where("id = ? AND decision_id = ?", optionID, decisionID).Count(&count).Error
	if err != nil {
		return false, core.Internal(fmt.Errorf("decisions: option check: %w", err))
	}
	return count > 0, nil
}

// SetStatus moves the decision through its lifecycle. Only the fields that the
// new status actually implies are written, so closing twice cannot overwrite
// the first closing time.
func (r *Repo) SetStatus(ctx context.Context, id core.ID, status Status, optionID core.ID, note string, at time.Time) (Decision, error) {
	current, err := r.ByID(ctx, id)
	if err != nil {
		return Decision{}, err
	}
	updates := map[string]any{"status": string(status), "updated_at": at}
	if !optionID.IsZero() {
		updates["resolved_option_id"] = optionID
	}
	if note != "" {
		updates["resolution_note"] = note
	}
	if current.ClosedAt == nil {
		switch status {
		case StatusClosed, StatusResolved, StatusCancelled:
			updates["closed_at"] = at
		}
	}
	if status == StatusResolved {
		updates["resolved_at"] = at
	}
	if err := r.db.WithContext(ctx).Model(&Decision{}).Where("id = ?", id).
		Updates(updates).Error; err != nil {
		return Decision{}, core.Internal(fmt.Errorf("decisions: set status: %w", err))
	}
	return r.ByID(ctx, id)
}

// DueForAutoClose returns open decisions whose deadline has passed, across all
// trips. The scheduler drives it.
func (r *Repo) DueForAutoClose(ctx context.Context, now time.Time, limit int) ([]Decision, error) {
	out := []Decision{}
	err := r.db.WithContext(ctx).
		Where("status = ? AND deadline IS NOT NULL AND deadline <= ?", string(StatusOpen), now).
		Order("deadline").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("decisions: due for close: %w", err))
	}
	return out, nil
}

// ClosingSoon returns open decisions whose deadline falls inside the window,
// for the "voting closes in 2 hours" reminder.
func (r *Repo) ClosingSoon(ctx context.Context, from, to time.Time) ([]Decision, error) {
	out := []Decision{}
	err := r.db.WithContext(ctx).
		Where("status = ? AND deadline IS NOT NULL AND deadline > ? AND deadline <= ?",
			string(StatusOpen), from, to).
		Order("deadline").Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("decisions: closing soon: %w", err))
	}
	return out, nil
}

// NonVoters lists active members of the trip who have not voted on a decision.
func (r *Repo) NonVoters(ctx context.Context, decisionID core.ID) ([]core.ID, error) {
	out := []core.ID{}
	err := r.db.WithContext(ctx).
		Table("decisions AS d").
		Joins("JOIN trip_members m ON m.trip_id = d.trip_id AND m.status = 'active'").
		Joins("LEFT JOIN decision_votes v ON v.decision_id = d.id AND v.member_id = m.id").
		Where("d.id = ? AND v.member_id IS NULL", decisionID).
		Pluck("m.user_id", &out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("decisions: non voters: %w", err))
	}
	return out, nil
}

// CountOpen is a dashboard counter.
func (r *Repo) CountOpen(ctx context.Context, tripID core.ID) (int, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Decision{}).
		Where("trip_id = ? AND status = ?", tripID, string(StatusOpen)).Count(&count).Error
	if err != nil {
		return 0, core.Internal(fmt.Errorf("decisions: count open: %w", err))
	}
	return int(count), nil
}
