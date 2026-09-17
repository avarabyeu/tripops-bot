package expenses

import (
	"sort"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Balance is what one member is owed (positive) or owes (negative) across the
// whole trip.
type Balance struct {
	MemberID    core.ID `json:"member_id"`
	DisplayName string  `json:"display_name"`
	// Paid is what they put on the table, Owed their share of everything.
	Paid core.Money `json:"paid_minor"`
	Owed core.Money `json:"owed_minor"`
	// Settled is the net of transfers already marked as done: money they have
	// handed over minus money they have received.
	Settled core.Money `json:"settled_minor"`
	Amount  core.Money `json:"balance_minor"`
}

// Transfer is a suggested payment from one member to another.
type Transfer struct {
	From     core.ID    `json:"from_member_id"`
	FromName string     `json:"from_name,omitempty"`
	To       core.ID    `json:"to_member_id"`
	ToName   string     `json:"to_name,omitempty"`
	Amount   core.Money `json:"amount_minor"`
	Currency string     `json:"currency"`
}

// MinimalTransfers turns a set of balances into a short list of payments that
// clears them.
//
// Finding the provably smallest set of transfers is NP-hard (it is partition
// in disguise), so this does the two things that matter in practice:
//
//  1. It pays off exact matches first. If Peter owes 13 and Kolya is owed 13,
//     that is one transfer and no residue, which greedy matching by size can
//     miss entirely.
//  2. It then greedily settles the largest debtor against the largest creditor.
//     Each step zeroes at least one participant, so with n non-zero balances
//     it never emits more than n-1 transfers.
//
// The result is deterministic: ties are broken by member id so the same
// balances always produce the same suggestions, which matters because the UI
// shows them as if they were stable facts.
//
// Input balances must sum to zero — they always do, because every expense's
// shares sum to its total. A non-zero sum means a bug upstream and is reported
// rather than silently absorbed.
func MinimalTransfers(balances []Balance, currency string) ([]Transfer, error) {
	type party struct {
		id     core.ID
		name   string
		amount core.Money // positive for creditors, negative for debtors
	}

	var sum core.Money
	var debtors, creditors []party
	for _, b := range balances {
		sum += b.Amount
		switch {
		case b.Amount > 0:
			creditors = append(creditors, party{id: b.MemberID, name: b.DisplayName, amount: b.Amount})
		case b.Amount < 0:
			debtors = append(debtors, party{id: b.MemberID, name: b.DisplayName, amount: b.Amount})
		}
	}
	if sum != 0 {
		return nil, core.Internal(errUnbalanced{sum})
	}
	if len(debtors) == 0 || len(creditors) == 0 {
		return []Transfer{}, nil
	}

	// Largest first, ties by id for determinism.
	sort.SliceStable(debtors, func(i, j int) bool {
		if debtors[i].amount != debtors[j].amount {
			return debtors[i].amount < debtors[j].amount // most negative first
		}
		return debtors[i].id.String() < debtors[j].id.String()
	})
	sort.SliceStable(creditors, func(i, j int) bool {
		if creditors[i].amount != creditors[j].amount {
			return creditors[i].amount > creditors[j].amount
		}
		return creditors[i].id.String() < creditors[j].id.String()
	})

	transfers := []Transfer{}
	emit := func(from, to party, amount core.Money) {
		transfers = append(transfers, Transfer{
			From: from.id, FromName: from.name,
			To: to.id, ToName: to.name,
			Amount: amount, Currency: currency,
		})
	}

	// Pass 1: exact matches.
	for i := range debtors {
		if debtors[i].amount == 0 {
			continue
		}
		for j := range creditors {
			if creditors[j].amount == 0 {
				continue
			}
			if -debtors[i].amount == creditors[j].amount {
				emit(debtors[i], creditors[j], creditors[j].amount)
				debtors[i].amount = 0
				creditors[j].amount = 0
				break
			}
		}
	}

	// Pass 2: greedy largest against largest.
	i, j := 0, 0
	for i < len(debtors) && j < len(creditors) {
		if debtors[i].amount == 0 {
			i++
			continue
		}
		if creditors[j].amount == 0 {
			j++
			continue
		}
		amount := min(-debtors[i].amount, creditors[j].amount)
		emit(debtors[i], creditors[j], amount)
		debtors[i].amount += amount
		creditors[j].amount -= amount
	}
	return transfers, nil
}

// errUnbalanced reports balances that do not sum to zero.
type errUnbalanced struct{ sum core.Money }

func (e errUnbalanced) Error() string {
	return "expenses: balances do not sum to zero (off by " + e.sum.String() + ")"
}

// SortBalances orders balances for display: biggest creditor first, biggest
// debtor last, ties by name.
func SortBalances(balances []Balance) {
	sort.SliceStable(balances, func(i, j int) bool {
		if balances[i].Amount != balances[j].Amount {
			return balances[i].Amount > balances[j].Amount
		}
		return balances[i].DisplayName < balances[j].DisplayName
	})
}
