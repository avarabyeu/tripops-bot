package expenses

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

// ---------------------------------------------------------------- expenses --

// expenseRow is an expense with the payer's name joined in.
type expenseRow struct {
	Expense
	JoinedPaidByName string
}

func (row expenseRow) expense() Expense {
	e := row.Expense
	e.PaidByName = row.JoinedPaidByName
	return e
}

func (r *Repo) query(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("expenses AS e").
		Select("e.*, coalesce(p.display_name, '') AS joined_paid_by_name").
		Joins("LEFT JOIN trip_members p ON p.id = e.paid_by")
}

func (r *Repo) Insert(ctx context.Context, e Expense) (Expense, error) {
	now := time.Now().UTC()
	e.CreatedAt, e.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&e).Error; err != nil {
		return Expense{}, core.Internal(fmt.Errorf("expenses: insert: %w", err))
	}
	return r.ByID(ctx, e.ID)
}

func (r *Repo) Update(ctx context.Context, e Expense) (Expense, error) {
	res := r.db.WithContext(ctx).Model(&Expense{}).Where("id = ?", e.ID).
		Updates(map[string]any{
			"title":        e.Title,
			"amount_minor": int64(e.Amount),
			"category":     string(e.Category),
			"paid_by":      e.PaidBy,
			"split_type":   string(e.SplitType),
			"spent_at":     e.SpentAt,
			"notes":        e.Notes,
			"updated_at":   time.Now().UTC(),
		})
	if res.Error != nil {
		return Expense{}, core.Internal(fmt.Errorf("expenses: update: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Expense{}, core.NotFound("expense")
	}
	return r.ByID(ctx, e.ID)
}

func (r *Repo) ByID(ctx context.Context, id core.ID) (Expense, error) {
	var row expenseRow
	if err := r.query(ctx).Where("e.id = ?", id).Limit(1).Scan(&row).Error; err != nil {
		return Expense{}, core.Internal(fmt.Errorf("expenses: by id: %w", err))
	}
	if row.ID.IsZero() {
		return Expense{}, core.NotFound("expense")
	}
	return row.expense(), nil
}

func (r *Repo) Delete(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Delete(&Expense{}, "id = ?", id)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("expenses: delete: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("expense")
	}
	return nil
}

func (r *Repo) ListByTrip(ctx context.Context, tripID core.ID) ([]Expense, error) {
	var rows []expenseRow
	err := r.query(ctx).Where("e.trip_id = ?", tripID).
		Order("e.spent_at DESC, e.created_at DESC").Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("expenses: list: %w", err))
	}
	out := make([]Expense, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.expense())
	}
	return out, nil
}

// SetShares replaces the participant rows of one expense.
func (r *Repo) SetShares(ctx context.Context, expenseID core.ID, shares []Share) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expense_id = ?", expenseID).Delete(&shareRow{}).Error; err != nil {
			return core.Internal(fmt.Errorf("expenses: clear shares: %w", err))
		}
		if len(shares) == 0 {
			return nil
		}
		rows := make([]shareRow, 0, len(shares))
		for _, s := range shares {
			rows = append(rows, shareRow{
				ExpenseID: expenseID, MemberID: s.MemberID, Share: s.Amount, Weight: s.Weight,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			if db.IsForeignKeyViolation(err) {
				return core.Invalid("participant is not a member of this trip")
			}
			return core.Internal(fmt.Errorf("expenses: insert shares: %w", err))
		}
		return nil
	})
}

// SharesByExpense loads the split of every expense of a trip.
func (r *Repo) SharesByExpense(ctx context.Context, tripID core.ID) (map[core.ID][]Share, error) {
	type row struct {
		ExpenseID   core.ID
		MemberID    core.ID
		DisplayName string
		ShareMinor  core.Money
		Weight      *int64
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("expense_participants AS ep").
		Select("ep.expense_id, ep.member_id, m.display_name, ep.share_minor, ep.weight").
		Joins("JOIN expenses e ON e.id = ep.expense_id").
		Joins("JOIN trip_members m ON m.id = ep.member_id").
		Where("e.trip_id = ?", tripID).
		Order("m.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("expenses: shares: %w", err))
	}
	out := map[core.ID][]Share{}
	for _, r := range rows {
		out[r.ExpenseID] = append(out[r.ExpenseID], Share{
			MemberID: r.MemberID, DisplayName: r.DisplayName, Amount: r.ShareMinor, Weight: r.Weight,
		})
	}
	return out, nil
}

// Balances computes every member's position: what they paid, what they owe,
// and the net of settlements already marked as done.
//
// The three figures come from correlated subqueries rather than a chain of
// joins, because joining a member to both their expenses and their settlements
// at once multiplies the rows and silently doubles the sums.
func (r *Repo) Balances(ctx context.Context, tripID core.ID) ([]Balance, error) {
	type row struct {
		MemberID    core.ID
		DisplayName string
		Paid        core.Money
		Owed        core.Money
		Settled     core.Money
	}
	var rows []row
	err := r.db.WithContext(ctx).
		Table("trip_members AS m").
		Select(`m.id AS member_id, m.display_name AS display_name,
			coalesce((SELECT sum(e.amount_minor) FROM expenses e
			          WHERE e.trip_id = @trip AND e.paid_by = m.id), 0) AS paid,
			coalesce((SELECT sum(ep.share_minor) FROM expense_participants ep
			          JOIN expenses e2 ON e2.id = ep.expense_id
			          WHERE e2.trip_id = @trip AND ep.member_id = m.id), 0) AS owed,
			coalesce((SELECT sum(s.amount_minor) FROM settlements s
			          WHERE s.trip_id = @trip AND s.status = 'settled' AND s.from_member_id = m.id), 0)
			- coalesce((SELECT sum(s2.amount_minor) FROM settlements s2
			          WHERE s2.trip_id = @trip AND s2.status = 'settled' AND s2.to_member_id = m.id), 0) AS settled`,
			map[string]any{"trip": tripID}).
		Where("m.trip_id = ? AND m.status <> ?", tripID, "removed").
		Order("m.created_at").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("expenses: balances: %w", err))
	}

	out := make([]Balance, 0, len(rows))
	for _, r := range rows {
		out = append(out, Balance{
			MemberID:    r.MemberID,
			DisplayName: r.DisplayName,
			Paid:        r.Paid,
			Owed:        r.Owed,
			Settled:     r.Settled,
			// Paying into the pot and handing cash to a creditor both reduce
			// what you still owe, so they add up the same way.
			Amount: r.Paid - r.Owed + r.Settled,
		})
	}
	return out, nil
}

// TripTotal is the headline number on the dashboard.
func (r *Repo) TripTotal(ctx context.Context, tripID core.ID) (core.Money, int, error) {
	var result struct {
		Total core.Money
		Count int
	}
	err := r.db.WithContext(ctx).Model(&Expense{}).
		Select("coalesce(sum(amount_minor), 0) AS total, count(*) AS count").
		Where("trip_id = ?", tripID).
		Scan(&result).Error
	if err != nil {
		return 0, 0, core.Internal(fmt.Errorf("expenses: trip total: %w", err))
	}
	return result.Total, result.Count, nil
}

// ------------------------------------------------------------- settlements --

// Settlement is a recorded transfer between two members. Only rows with status
// "settled" move a balance; "pending" rows are plans.
type Settlement struct {
	ID       core.ID    `json:"id"      gorm:"primaryKey"`
	TripID   core.ID    `json:"trip_id" gorm:"not null;index:idx_settlements_trip,priority:1"`
	From     core.ID    `json:"from_member_id" gorm:"column:from_member_id;not null"`
	FromName string     `json:"from_name"      gorm:"-"`
	To       core.ID    `json:"to_member_id"   gorm:"column:to_member_id;not null"`
	ToName   string     `json:"to_name"        gorm:"-"`
	Amount   core.Money `json:"amount_minor"   gorm:"column:amount_minor;not null"`
	Currency string     `json:"currency" gorm:"size:3;not null"`
	Status   string     `json:"status"   gorm:"size:16;not null;default:'pending';index:idx_settlements_trip,priority:2"`
	Note     string     `json:"note,omitempty" gorm:"size:200;not null;default:''"`

	CreatedBy core.ID    `json:"created_by" gorm:"not null"`
	CreatedAt time.Time  `json:"created_at" gorm:"not null"`
	SettledAt *time.Time `json:"settled_at,omitempty"`
	SettledBy core.ID    `json:"-"`
}

func (Settlement) TableName() string { return "settlements" }

// Settlement statuses.
const (
	SettlementPending   = "pending"
	SettlementSettled   = "settled"
	SettlementCancelled = "cancelled"
)

type settlementRow struct {
	Settlement
	JoinedFromName string
	JoinedToName   string
}

func (row settlementRow) settlement() Settlement {
	s := row.Settlement
	s.FromName = row.JoinedFromName
	s.ToName = row.JoinedToName
	return s
}

func (r *Repo) settlementQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("settlements AS s").
		Select(`s.*, coalesce(f.display_name, '') AS joined_from_name,
			coalesce(t.display_name, '') AS joined_to_name`).
		Joins("LEFT JOIN trip_members f ON f.id = s.from_member_id").
		Joins("LEFT JOIN trip_members t ON t.id = s.to_member_id")
}

func (r *Repo) InsertSettlement(ctx context.Context, s Settlement) (Settlement, error) {
	s.CreatedAt = time.Now().UTC()
	if s.Status == SettlementSettled {
		now := s.CreatedAt
		s.SettledAt = &now
		s.SettledBy = s.CreatedBy
	}
	if err := r.db.WithContext(ctx).Create(&s).Error; err != nil {
		return Settlement{}, core.Internal(fmt.Errorf("expenses: insert settlement: %w", err))
	}
	return r.SettlementByID(ctx, s.ID)
}

func (r *Repo) SettlementByID(ctx context.Context, id core.ID) (Settlement, error) {
	var row settlementRow
	if err := r.settlementQuery(ctx).Where("s.id = ?", id).Limit(1).Scan(&row).Error; err != nil {
		return Settlement{}, core.Internal(fmt.Errorf("expenses: settlement by id: %w", err))
	}
	if row.ID.IsZero() {
		return Settlement{}, core.NotFound("settlement")
	}
	return row.settlement(), nil
}

func (r *Repo) ListSettlements(ctx context.Context, tripID core.ID) ([]Settlement, error) {
	var rows []settlementRow
	err := r.settlementQuery(ctx).
		Where("s.trip_id = ? AND s.status <> ?", tripID, SettlementCancelled).
		Order("s.created_at DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("expenses: list settlements: %w", err))
	}
	out := make([]Settlement, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.settlement())
	}
	return out, nil
}

// MarkSettled flips a pending settlement to settled. It is a no-op on a row
// that is already settled, so a double tap on the button is harmless.
func (r *Repo) MarkSettled(ctx context.Context, id, byUser core.ID) (Settlement, error) {
	err := r.db.WithContext(ctx).Model(&Settlement{}).
		Where("id = ? AND status = ?", id, SettlementPending).
		Updates(map[string]any{
			"status":     SettlementSettled,
			"settled_at": time.Now().UTC(),
			"settled_by": byUser,
		}).Error
	if err != nil {
		return Settlement{}, core.Internal(fmt.Errorf("expenses: mark settled: %w", err))
	}
	return r.SettlementByID(ctx, id)
}

func (r *Repo) CancelSettlement(ctx context.Context, id core.ID) error {
	res := r.db.WithContext(ctx).Model(&Settlement{}).
		Where("id = ? AND status <> ?", id, SettlementCancelled).
		Update("status", SettlementCancelled)
	if res.Error != nil {
		return core.Internal(fmt.Errorf("expenses: cancel settlement: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("settlement")
	}
	return nil
}
