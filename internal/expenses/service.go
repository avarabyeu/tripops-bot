package expenses

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/trips"
)

type Service struct {
	database *db.DB
	repo     *Repo
	trips    *trips.Service
	activity activity.Logger
	notify   notify.Enqueuer
}

func NewService(database *db.DB, tripSvc *trips.Service, act activity.Logger, notifier notify.Enqueuer) *Service {
	return &Service{database: database, repo: NewRepo(database.DB), trips: tripSvc, activity: act, notify: notifier}
}

func (s *Service) Repo() *Repo { return s.repo }

// Input records a payment. Amounts are minor units of the trip currency; the
// API never accepts a decimal string for money.
type Input struct {
	Title    string     `json:"title"`
	Amount   core.Money `json:"amount_minor"`
	Category Category   `json:"category"`
	// PaidBy defaults to the caller: most expenses are recorded by whoever paid.
	PaidBy    core.ID   `json:"paid_by"`
	SplitType SplitType `json:"split_type"`
	// Participants defaults to every active member with an equal split.
	Participants []Participant `json:"participants"`
	SpentAt      *time.Time    `json:"spent_at"`
	Notes        string        `json:"notes"`
}

// List returns the trip's expenses with their splits attached.
func (s *Service) List(ctx context.Context, access trips.Access) ([]Expense, error) {
	list, err := s.repo.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	shares, err := s.repo.SharesByExpense(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Shares = shares[list[i].ID]
		if list[i].Shares == nil {
			list[i].Shares = []Share{}
		}
	}
	return list, nil
}

// Create records an expense. Any active participant may add one: in a group of
// friends everybody pays for something, and making that an organiser-only
// action guarantees the ledger is incomplete.
func (s *Service) Create(ctx context.Context, access trips.Access, in Input) (Expense, error) {
	if err := access.RequireWrite(); err != nil {
		return Expense{}, err
	}
	expense, shares, err := s.build(ctx, access, Expense{
		ID:        core.NewID(),
		TripID:    access.Trip.ID,
		Currency:  access.Trip.Currency,
		CreatedBy: access.User.ID,
	}, in)
	if err != nil {
		return Expense{}, err
	}

	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.Insert(ctx, expense)
		if err != nil {
			return err
		}
		expense = created
		return repo.SetShares(ctx, expense.ID, shares)
	})
	if err != nil {
		return Expense{}, err
	}

	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindExpenseAdded,
		Message: fmt.Sprintf("%s added %s %s", access.Member.DisplayName, expense.Formatted(), expense.Title),
		Meta:    map[string]any{"expense_id": expense.ID.String()},
	})
	s.fanOut(ctx, access, notify.Notification{
		TripID:   access.Trip.ID,
		Category: notify.CategoryExpenses,
		Title:    "💰 " + access.Trip.Title,
		Body: fmt.Sprintf("%s %s — %s, paid by %s", expense.Category.Icon(), expense.Title,
			expense.Formatted(), expense.PaidByName),
		Payload: map[string]any{"expense_id": expense.ID.String()},
	})
	return s.get(ctx, access, expense.ID)
}

// Patch updates an expense. Any field left nil keeps its value; passing
// participants replaces the whole split.
type Patch struct {
	Title        *string        `json:"title"`
	Amount       *core.Money    `json:"amount_minor"`
	Category     *Category      `json:"category"`
	PaidBy       *core.ID       `json:"paid_by"`
	SplitType    *SplitType     `json:"split_type"`
	Participants *[]Participant `json:"participants"`
	SpentAt      *time.Time     `json:"spent_at"`
	Notes        *string        `json:"notes"`
}

// Update edits an expense. The person who recorded it and organisers may edit;
// everyone else would be rewriting somebody's receipt.
func (s *Service) Update(ctx context.Context, access trips.Access, id core.ID, in Patch) (Expense, error) {
	if err := access.RequireWrite(); err != nil {
		return Expense{}, err
	}
	existing, err := s.loadWithShares(ctx, access, id)
	if err != nil {
		return Expense{}, err
	}
	if existing.CreatedBy != access.User.ID && !access.IsManager() {
		return Expense{}, core.Forbidden("only the person who added this expense or an organiser can edit it")
	}

	merged := Input{
		Title:     existing.Title,
		Amount:    existing.Amount,
		Category:  existing.Category,
		PaidBy:    existing.PaidBy,
		SplitType: existing.SplitType,
		Notes:     existing.Notes,
		SpentAt:   &existing.SpentAt,
	}
	// Reuse the stored split unless the caller replaces it, converting shares
	// back into the weights they came from.
	merged.Participants = make([]Participant, 0, len(existing.Shares))
	for _, sh := range existing.Shares {
		p := Participant{MemberID: sh.MemberID}
		if sh.Weight != nil {
			p.Weight = *sh.Weight
		}
		merged.Participants = append(merged.Participants, p)
	}

	if in.Title != nil {
		merged.Title = *in.Title
	}
	if in.Amount != nil {
		merged.Amount = *in.Amount
	}
	if in.Category != nil {
		merged.Category = *in.Category
	}
	if in.PaidBy != nil {
		merged.PaidBy = *in.PaidBy
	}
	if in.SplitType != nil {
		merged.SplitType = *in.SplitType
	}
	if in.Participants != nil {
		merged.Participants = *in.Participants
	}
	if in.SpentAt != nil {
		merged.SpentAt = in.SpentAt
	}
	if in.Notes != nil {
		merged.Notes = *in.Notes
	}
	// An amount change with an untouched custom split can no longer add up;
	// fall back to an equal split rather than rejecting the edit.
	if in.Amount != nil && in.Participants == nil && merged.SplitType == SplitCustomAmount {
		var sum int64
		for _, p := range merged.Participants {
			sum += p.Weight
		}
		if sum != int64(merged.Amount) {
			merged.SplitType = SplitEqual
		}
	}

	expense, shares, err := s.build(ctx, access, existing, merged)
	if err != nil {
		return Expense{}, err
	}
	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		updated, err := repo.Update(ctx, expense)
		if err != nil {
			return err
		}
		expense = updated
		return repo.SetShares(ctx, expense.ID, shares)
	})
	if err != nil {
		return Expense{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindExpenseUpdated,
		Message: fmt.Sprintf("%s updated %q", access.Member.DisplayName, expense.Title),
	})
	return s.get(ctx, access, expense.ID)
}

func (s *Service) Delete(ctx context.Context, access trips.Access, id core.ID) error {
	if err := access.RequireWrite(); err != nil {
		return err
	}
	expense, err := s.load(ctx, access, id)
	if err != nil {
		return err
	}
	if expense.CreatedBy != access.User.ID && !access.IsManager() {
		return core.Forbidden("only the person who added this expense or an organiser can delete it")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindExpenseDeleted,
		Message: fmt.Sprintf("%s removed %q", access.Member.DisplayName, expense.Title),
	})
	return nil
}

// ------------------------------------------------------------- settlements --

// BalanceReport is everything the "who owes whom" screen needs.
type BalanceReport struct {
	Currency    string       `json:"currency"`
	Total       core.Money   `json:"total_minor"`
	Balances    []Balance    `json:"balances"`
	Transfers   []Transfer   `json:"transfers"`
	Settlements []Settlement `json:"settlements"`
}

// Balances computes positions and the suggested transfers to clear them.
func (s *Service) Balances(ctx context.Context, access trips.Access) (BalanceReport, error) {
	balances, err := s.repo.Balances(ctx, access.Trip.ID)
	if err != nil {
		return BalanceReport{}, err
	}
	transfers, err := MinimalTransfers(balances, access.Trip.Currency)
	if err != nil {
		return BalanceReport{}, err
	}
	settlements, err := s.repo.ListSettlements(ctx, access.Trip.ID)
	if err != nil {
		return BalanceReport{}, err
	}
	total, _, err := s.repo.TripTotal(ctx, access.Trip.ID)
	if err != nil {
		return BalanceReport{}, err
	}
	SortBalances(balances)
	return BalanceReport{
		Currency:    access.Trip.Currency,
		Total:       total,
		Balances:    balances,
		Transfers:   transfers,
		Settlements: settlements,
	}, nil
}

// SettlementInput records a transfer between two members.
type SettlementInput struct {
	From   core.ID    `json:"from_member_id"`
	To     core.ID    `json:"to_member_id"`
	Amount core.Money `json:"amount_minor"`
	Note   string     `json:"note"`
	// Pending records the intention without moving the balance. The default —
	// and what the "Mark as settled" button sends — is a completed transfer.
	Pending bool `json:"pending"`
}

// RecordSettlement writes down that money changed hands. Either party to the
// transfer may record it, as may an organiser.
func (s *Service) RecordSettlement(ctx context.Context, access trips.Access, in SettlementInput) (Settlement, error) {
	if err := access.RequireWrite(); err != nil {
		return Settlement{}, err
	}
	v := core.NewValidator()
	v.Check(!in.From.IsZero(), "from_member_id", "is required")
	v.Check(!in.To.IsZero(), "to_member_id", "is required")
	v.Check(in.From != in.To, "to_member_id", "must differ from the payer")
	v.Check(in.Amount > 0, "amount_minor", "must be greater than zero")
	v.Length(in.Note, "note", 0, 200)
	if err := v.Err(); err != nil {
		return Settlement{}, err
	}
	from, err := s.trips.MemberByID(ctx, access.Trip.ID, in.From)
	if err != nil {
		return Settlement{}, err
	}
	to, err := s.trips.MemberByID(ctx, access.Trip.ID, in.To)
	if err != nil {
		return Settlement{}, err
	}
	involved := access.Member.ID == in.From || access.Member.ID == in.To
	if !involved && !access.IsManager() {
		return Settlement{}, core.Forbidden("only the two people involved or an organiser can record this")
	}

	status := SettlementSettled
	if in.Pending {
		status = SettlementPending
	}
	settlement, err := s.repo.InsertSettlement(ctx, Settlement{
		ID: core.NewID(), TripID: access.Trip.ID, From: in.From, To: in.To,
		Amount: in.Amount, Currency: access.Trip.Currency, Status: status,
		Note: strings.TrimSpace(in.Note), CreatedBy: access.User.ID,
	})
	if err != nil {
		return Settlement{}, err
	}
	if status == SettlementSettled {
		s.logSettled(ctx, access, from.DisplayName, to.DisplayName, settlement)
	}
	return settlement, nil
}

// MarkSettled flips a planned transfer to done.
func (s *Service) MarkSettled(ctx context.Context, access trips.Access, id core.ID) (Settlement, error) {
	if err := access.RequireWrite(); err != nil {
		return Settlement{}, err
	}
	settlement, err := s.repo.SettlementByID(ctx, id)
	if err != nil {
		return Settlement{}, err
	}
	if settlement.TripID != access.Trip.ID {
		return Settlement{}, core.NotFound("settlement")
	}
	involved := access.Member.ID == settlement.From || access.Member.ID == settlement.To
	if !involved && !access.IsManager() {
		return Settlement{}, core.Forbidden("only the two people involved or an organiser can settle this")
	}
	if settlement.Status == SettlementSettled {
		return settlement, nil
	}
	updated, err := s.repo.MarkSettled(ctx, id, access.User.ID)
	if err != nil {
		return Settlement{}, err
	}
	s.logSettled(ctx, access, updated.FromName, updated.ToName, updated)
	return updated, nil
}

// CancelSettlement removes a transfer that never happened.
func (s *Service) CancelSettlement(ctx context.Context, access trips.Access, id core.ID) error {
	if err := access.RequireWrite(); err != nil {
		return err
	}
	settlement, err := s.repo.SettlementByID(ctx, id)
	if err != nil {
		return err
	}
	if settlement.TripID != access.Trip.ID {
		return core.NotFound("settlement")
	}
	involved := access.Member.ID == settlement.From || access.Member.ID == settlement.To
	if !involved && !access.IsManager() {
		return core.Forbidden("only the two people involved or an organiser can cancel this")
	}
	return s.repo.CancelSettlement(ctx, id)
}

func (s *Service) logSettled(ctx context.Context, access trips.Access, fromName, toName string, st Settlement) {
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindSettlementMarked,
		Message: fmt.Sprintf("%s paid %s %s", fromName, toName, st.Amount.Format(st.Currency)),
	})
}

// ----------------------------------------------------------------- helpers --

// build validates an expense and computes its split.
func (s *Service) build(ctx context.Context, access trips.Access, base Expense, in Input) (Expense, []Share, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.Category == "" {
		in.Category = CategoryOther
	}
	if in.SplitType == "" {
		in.SplitType = SplitEqual
	}
	if in.PaidBy.IsZero() {
		in.PaidBy = access.Member.ID
	}

	v := core.NewValidator()
	v.Required(in.Title, "title")
	v.Length(in.Title, "title", 1, 120)
	v.Length(in.Notes, "notes", 0, 1000)
	v.Check(in.Amount > 0, "amount_minor", "must be greater than zero")
	v.Check(in.Amount < 100_000_000, "amount_minor", "is implausibly large")
	v.Check(in.Category.Valid(), "category", "must be one of: "+strings.Join(CategoryStrings(), ", "))
	v.Check(in.SplitType.Valid(), "split_type", "must be one of: "+strings.Join(SplitTypeStrings(), ", "))
	if err := v.Err(); err != nil {
		return Expense{}, nil, err
	}

	members, err := s.trips.Members(ctx, access)
	if err != nil {
		return Expense{}, nil, err
	}
	names := map[core.ID]string{}
	active := make([]core.ID, 0, len(members))
	for _, m := range members {
		names[m.ID] = m.DisplayName
		if m.Active() {
			active = append(active, m.ID)
		}
	}
	if _, ok := names[in.PaidBy]; !ok {
		return Expense{}, nil, core.Invalid("paid_by is not a participant of this trip")
	}

	participants := in.Participants
	if len(participants) == 0 {
		if in.SplitType != SplitEqual {
			return Expense{}, nil, core.Invalid("a %s split needs an explicit participant list", in.SplitType)
		}
		participants = make([]Participant, 0, len(active))
		for _, id := range active {
			participants = append(participants, Participant{MemberID: id})
		}
	}
	for _, p := range participants {
		if _, ok := names[p.MemberID]; !ok {
			return Expense{}, nil, core.Invalid("participants contains someone who is not on this trip")
		}
	}

	shares, err := ComputeShares(in.Amount, in.SplitType, participants)
	if err != nil {
		return Expense{}, nil, err
	}
	for i := range shares {
		shares[i].DisplayName = names[shares[i].MemberID]
	}
	sortShares(shares)

	spentAt := time.Now().UTC()
	if in.SpentAt != nil {
		spentAt = core.UTC(*in.SpentAt)
	}

	expense := base
	expense.Title = in.Title
	expense.Amount = in.Amount
	expense.Category = in.Category
	expense.PaidBy = in.PaidBy
	expense.PaidByName = names[in.PaidBy]
	expense.SplitType = in.SplitType
	expense.SpentAt = spentAt
	expense.Notes = in.Notes
	if expense.Currency == "" {
		expense.Currency = access.Trip.Currency
	}
	return expense, shares, nil
}

func (s *Service) load(ctx context.Context, access trips.Access, id core.ID) (Expense, error) {
	expense, err := s.repo.ByID(ctx, id)
	if err != nil {
		return Expense{}, err
	}
	if expense.TripID != access.Trip.ID {
		return Expense{}, core.NotFound("expense")
	}
	return expense, nil
}

func (s *Service) loadWithShares(ctx context.Context, access trips.Access, id core.ID) (Expense, error) {
	expense, err := s.load(ctx, access, id)
	if err != nil {
		return Expense{}, err
	}
	shares, err := s.repo.SharesByExpense(ctx, access.Trip.ID)
	if err != nil {
		return Expense{}, err
	}
	expense.Shares = shares[id]
	return expense, nil
}

func (s *Service) get(ctx context.Context, access trips.Access, id core.ID) (Expense, error) {
	return s.loadWithShares(ctx, access, id)
}

func (s *Service) fanOut(ctx context.Context, access trips.Access, template notify.Notification) {
	userIDs, err := s.trips.ActiveMemberUserIDs(ctx, access.Trip.ID)
	if err != nil {
		return
	}
	items := make([]notify.Notification, 0, len(userIDs))
	for _, id := range userIDs {
		if id == access.User.ID {
			continue
		}
		n := template
		n.UserID = id
		items = append(items, n)
	}
	_ = s.notify.Enqueue(ctx, items...)
}
