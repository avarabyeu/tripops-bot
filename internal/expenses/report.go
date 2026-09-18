package expenses

import (
	"sort"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// MemberSummary is one person's line in the report: what they put in, what
// their share of everything came to, and what that leaves.
type MemberSummary struct {
	MemberID    core.ID `json:"member_id"`
	DisplayName string  `json:"display_name"`
	// Paid is what they put on the table; PaidCount how many bills that was.
	Paid      core.Money `json:"paid_minor"`
	PaidCount int        `json:"paid_count"`
	// Share is their part of everything the group spent, however each expense
	// was split, and ShareCount how many bills they are on.
	Share      core.Money `json:"share_minor"`
	ShareCount int        `json:"share_count"`
	// Settled is the net of transfers already marked as done, and Balance what
	// remains: positive means the group owes them.
	Settled core.Money `json:"settled_minor"`
	Balance core.Money `json:"balance_minor"`
}

// CategoryTotal is one line of the "where it went" breakdown.
type CategoryTotal struct {
	Category Category   `json:"category"`
	Total    core.Money `json:"total_minor"`
	Count    int        `json:"count"`
	// Percent of the trip total, rounded, so the client can draw a bar without
	// doing money arithmetic of its own.
	Percent int `json:"percent"`
}

// Report is the whole ledger in one answer: what the trip cost, where it went,
// and where each person stands.
//
// Nothing here is stored. It is derived from the same expense shares the
// balances screen reads, so the two cannot disagree: Members[i].Balance is the
// balance, restated alongside the paid and share figures it came from.
type Report struct {
	Currency string     `json:"currency"`
	Total    core.Money `json:"total_minor"`
	Count    int        `json:"count"`
	// PerPerson is the total divided by the number of people on the trip —
	// what it cost on average, which is not what anyone owes.
	PerPerson  core.Money      `json:"per_person_minor"`
	ByCategory []CategoryTotal `json:"by_category"`
	Members    []MemberSummary `json:"members"`
	Expenses   []Expense       `json:"expenses"`
}

// BuildReport assembles the report from a trip's expenses — shares attached —
// and the balances computed for them.
//
// It is pure: the counts come from walking the expenses, the money from the
// balances. That split is deliberate. Re-deriving the amounts here would mean
// two implementations of "what does everyone owe" that could drift apart.
func BuildReport(list []Expense, balances []Balance, currency string) Report {
	report := Report{Currency: currency, Count: len(list), Expenses: list}
	if report.Expenses == nil {
		report.Expenses = []Expense{}
	}

	byCategory := map[Category]*CategoryTotal{}
	paidCount := map[core.ID]int{}
	shareCount := map[core.ID]int{}

	for _, e := range list {
		report.Total += e.Amount

		line, seen := byCategory[e.Category]
		if !seen {
			line = &CategoryTotal{Category: e.Category}
			byCategory[e.Category] = line
		}
		line.Total += e.Amount
		line.Count++

		paidCount[e.PaidBy]++
		for _, share := range e.Shares {
			// A zero share happens with a custom split that gives somebody
			// nothing; they are not "on" that bill in any useful sense.
			if share.Amount != 0 {
				shareCount[share.MemberID]++
			}
		}
	}

	report.ByCategory = make([]CategoryTotal, 0, len(byCategory))
	for _, line := range byCategory {
		if report.Total > 0 {
			line.Percent = int((int64(line.Total)*100 + int64(report.Total)/2) / int64(report.Total))
		}
		report.ByCategory = append(report.ByCategory, *line)
	}
	// Biggest spend first — the question is "where did the money go", and the
	// name breaks ties so the order does not wobble between requests.
	sort.SliceStable(report.ByCategory, func(i, j int) bool {
		if report.ByCategory[i].Total != report.ByCategory[j].Total {
			return report.ByCategory[i].Total > report.ByCategory[j].Total
		}
		return report.ByCategory[i].Category < report.ByCategory[j].Category
	})

	report.Members = make([]MemberSummary, 0, len(balances))
	for _, b := range balances {
		report.Members = append(report.Members, MemberSummary{
			MemberID:    b.MemberID,
			DisplayName: b.DisplayName,
			Paid:        b.Paid,
			PaidCount:   paidCount[b.MemberID],
			Share:       b.Owed,
			ShareCount:  shareCount[b.MemberID],
			Settled:     b.Settled,
			Balance:     b.Amount,
		})
	}
	// Owed the most at the top, owing the most at the bottom: the same order
	// as the balances screen, so the two read as one thing.
	sort.SliceStable(report.Members, func(i, j int) bool {
		if report.Members[i].Balance != report.Members[j].Balance {
			return report.Members[i].Balance > report.Members[j].Balance
		}
		return report.Members[i].DisplayName < report.Members[j].DisplayName
	})

	if n := len(report.Members); n > 0 {
		report.PerPerson = core.Money(int64(report.Total) / int64(n))
	}
	return report
}
