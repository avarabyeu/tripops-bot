// Package expenses tracks who paid for what and works out who owes whom.
//
// Money never touches a float in this package. Every amount is an integer
// number of minor units (cents) of the trip currency, splits distribute the
// remainder cent by cent, and the shares of an expense always sum exactly back
// to its total. That property is what makes balances trustworthy, and it is
// asserted in the tests.
//
// There is no payment-provider integration and none is planned for the MVP:
// the product records what happened, it does not move money.
package expenses

import (
	"slices"
	"sort"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Category classifies spending. The set is fixed; "other" absorbs the rest.
type Category string

const (
	CategoryAccommodation Category = "accommodation"
	CategoryTransport     Category = "transport"
	CategoryFuel          Category = "fuel"
	CategoryFood          Category = "food"
	CategoryParking       Category = "parking"
	CategoryRegistration  Category = "registration"
	CategoryEquipment     Category = "equipment"
	CategoryOther         Category = "other"
)

var allCategories = []Category{
	CategoryAccommodation, CategoryTransport, CategoryFuel,
	CategoryFood, CategoryParking, CategoryRegistration, CategoryEquipment, CategoryOther,
}

func (c Category) Valid() bool {
	return slices.Contains(allCategories, c)
}

func (c Category) Icon() string {
	switch c {
	case CategoryAccommodation:
		return "🏠"
	case CategoryTransport:
		return "🚗"
	case CategoryFuel:
		return "⛽"
	case CategoryFood:
		return "🍽"
	case CategoryParking:
		return "🅿️"
	case CategoryRegistration:
		return "🎫"
	case CategoryEquipment:
		return "🧰"
	default:
		return "💶"
	}
}

func CategoryStrings() []string {
	out := make([]string, len(allCategories))
	for i, c := range allCategories {
		out[i] = string(c)
	}
	return out
}

// SplitType is how an expense is divided.
type SplitType string

const (
	// SplitEqual divides the total evenly, handing leftover cents to the first
	// participants so the shares still sum to the total.
	SplitEqual SplitType = "equal"
	// SplitCustomAmount takes an explicit amount per participant; they must add
	// up to the total exactly.
	SplitCustomAmount SplitType = "custom_amount"
	// SplitPercentage takes a percentage per participant in basis points
	// (10000 = 100%); they must add up to 100%.
	SplitPercentage SplitType = "percentage"
)

func (s SplitType) Valid() bool {
	switch s {
	case SplitEqual, SplitCustomAmount, SplitPercentage:
		return true
	}
	return false
}

func SplitTypeStrings() []string {
	return []string{string(SplitEqual), string(SplitCustomAmount), string(SplitPercentage)}
}

// BasisPoints is the unit percentages are expressed in: 10000 = 100%.
const BasisPoints int64 = 10000

// Share is one participant's part of an expense.
type Share struct {
	MemberID    core.ID    `json:"member_id"`
	DisplayName string     `json:"display_name,omitempty"`
	Amount      core.Money `json:"share_minor"`
	// Weight is the input the share was derived from: minor units for a custom
	// split, basis points for a percentage split, absent for an equal one.
	Weight *int64 `json:"weight,omitempty"`
}

// Participant is the split input for one member.
type Participant struct {
	MemberID core.ID `json:"member_id"`
	// Weight means minor units for custom_amount and basis points for
	// percentage. It is ignored for an equal split.
	Weight int64 `json:"weight"`
}

// Expense is one thing somebody paid for.
type Expense struct {
	ID       core.ID    `json:"id"      gorm:"primaryKey"`
	TripID   core.ID    `json:"trip_id" gorm:"not null;index:idx_expenses_trip,priority:1"`
	Title    string     `json:"title"        gorm:"size:120;not null"`
	Amount   core.Money `json:"amount_minor" gorm:"column:amount_minor;not null"`
	Currency string     `json:"currency"     gorm:"size:3;not null"`
	Category Category   `json:"category"     gorm:"size:20;not null;default:'other'"`

	PaidBy     core.ID `json:"paid_by" gorm:"not null"`
	PaidByName string  `json:"paid_by_name,omitempty" gorm:"-"`

	SplitType SplitType `json:"split_type" gorm:"size:20;not null;default:'equal'"`
	SpentAt   time.Time `json:"spent_at"   gorm:"not null;index:idx_expenses_trip,priority:2,sort:desc"`
	Notes     string    `json:"notes,omitempty" gorm:"size:1000;not null;default:''"`
	CreatedBy core.ID   `json:"created_by" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`

	Shares []Share `json:"participants" gorm:"-"`
}

func (Expense) TableName() string { return "expenses" }

// shareRow is one participant's stored part of an expense. The rows of an
// expense always sum to its total, whatever the split type, which is what
// makes the balance query a plain SUM.
type shareRow struct {
	ExpenseID core.ID    `gorm:"primaryKey"`
	MemberID  core.ID    `gorm:"primaryKey"`
	Share     core.Money `gorm:"column:share_minor;not null"`
	// Weight is the input the share was derived from: minor units for a custom
	// split, basis points for a percentage split, NULL for an equal one.
	Weight *int64
}

func (shareRow) TableName() string { return "expense_participants" }

// Formatted renders the amount with the trip currency, e.g. "€120.00".
func (e Expense) Formatted() string { return e.Amount.Format(e.Currency) }

// ComputeShares turns a split specification into exact per-member amounts.
//
// Whatever the split type, the returned shares sum to total. Callers rely on
// that: the balance query is a plain SUM over the shares and would silently
// drift if a single cent went missing.
func ComputeShares(total core.Money, split SplitType, participants []Participant) ([]Share, error) {
	if total <= 0 {
		return nil, core.Invalid("amount must be greater than zero")
	}
	if len(participants) == 0 {
		return nil, core.Invalid("an expense needs at least one participant")
	}
	seen := map[core.ID]bool{}
	for _, p := range participants {
		if p.MemberID.IsZero() {
			return nil, core.Invalid("participant is missing a member id")
		}
		if seen[p.MemberID] {
			return nil, core.Invalid("the same participant is listed twice")
		}
		seen[p.MemberID] = true
	}

	shares := make([]Share, len(participants))
	for i, p := range participants {
		shares[i] = Share{MemberID: p.MemberID}
	}

	switch split {
	case SplitEqual:
		amounts := core.DistributeEqually(total, len(participants))
		for i := range shares {
			shares[i].Amount = amounts[i]
		}

	case SplitCustomAmount:
		var sum int64
		for i, p := range participants {
			if p.Weight < 0 {
				return nil, core.Invalid("a custom share cannot be negative")
			}
			sum += p.Weight
			weight := p.Weight
			shares[i].Amount = core.Money(weight)
			shares[i].Weight = &weight
		}
		if sum != int64(total) {
			return nil, core.Invalid("the shares add up to %s but the expense is %s",
				core.Money(sum).String(), total.String())
		}

	case SplitPercentage:
		var sum int64
		weights := make([]int64, len(participants))
		for i, p := range participants {
			if p.Weight < 0 {
				return nil, core.Invalid("a percentage cannot be negative")
			}
			weights[i] = p.Weight
			sum += p.Weight
		}
		if sum != BasisPoints {
			return nil, core.Invalid("the percentages add up to %.2f%% instead of 100%%",
				float64(sum)/100)
		}
		amounts, err := core.DistributeByWeight(total, weights)
		if err != nil {
			return nil, core.Invalid("%s", err.Error())
		}
		for i := range shares {
			shares[i].Amount = amounts[i]
			weight := weights[i]
			shares[i].Weight = &weight
		}

	default:
		return nil, core.Invalid("split_type must be one of: equal, custom_amount, percentage")
	}

	return shares, nil
}

// Totals summarises a trip's spending for the dashboard.
type Totals struct {
	Total      core.Money              `json:"total_minor"`
	Currency   string                  `json:"currency"`
	ByCategory map[Category]core.Money `json:"by_category"`
	Count      int                     `json:"count"`
}

// SummariseTotals adds up a list of expenses.
func SummariseTotals(list []Expense, currency string) Totals {
	t := Totals{Currency: currency, ByCategory: map[Category]core.Money{}, Count: len(list)}
	for _, e := range list {
		t.Total += e.Amount
		t.ByCategory[e.Category] += e.Amount
	}
	return t
}

// sortShares keeps output order stable regardless of map iteration.
func sortShares(shares []Share) {
	sort.SliceStable(shares, func(i, j int) bool {
		return shares[i].MemberID.String() < shares[j].MemberID.String()
	})
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Expense{}, &shareRow{}, &Settlement{}} }
