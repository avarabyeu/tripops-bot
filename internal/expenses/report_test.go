package expenses_test

import (
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
)

func TestBuildReportSummarisesSpendAndPeople(t *testing.T) {
	ann, bob, cid := core.NewID(), core.NewID(), core.NewID()

	list := []expenses.Expense{
		{
			ID: core.NewID(), Title: "Fuel", Amount: 9000, Category: expenses.CategoryFuel,
			PaidBy: ann, Shares: []expenses.Share{
				{MemberID: ann, Amount: 3000}, {MemberID: bob, Amount: 3000}, {MemberID: cid, Amount: 3000},
			},
		},
		{
			ID: core.NewID(), Title: "Hotel", Amount: 21000, Category: expenses.CategoryAccommodation,
			PaidBy: bob, Shares: []expenses.Share{
				{MemberID: ann, Amount: 7000}, {MemberID: bob, Amount: 7000}, {MemberID: cid, Amount: 7000},
			},
		},
		{
			// Only two people ate; the third is not on this bill at all.
			ID: core.NewID(), Title: "Dinner", Amount: 6000, Category: expenses.CategoryFood,
			PaidBy: ann, Shares: []expenses.Share{
				{MemberID: ann, Amount: 3000}, {MemberID: bob, Amount: 3000},
			},
		},
	}
	balances := []expenses.Balance{
		{MemberID: ann, DisplayName: "Ann", Paid: 15000, Owed: 13000, Amount: 2000},
		{MemberID: bob, DisplayName: "Bob", Paid: 21000, Owed: 13000, Amount: 8000},
		{MemberID: cid, DisplayName: "Cid", Paid: 0, Owed: 10000, Amount: -10000},
	}

	report := expenses.BuildReport(list, balances, "EUR")

	if report.Total != 36000 {
		t.Fatalf("total = %d, want 36000", report.Total)
	}
	if report.Count != 3 {
		t.Fatalf("count = %d, want 3", report.Count)
	}
	if report.PerPerson != 12000 {
		t.Fatalf("per person = %d, want 12000", report.PerPerson)
	}

	// Biggest category first.
	if got := report.ByCategory[0].Category; got != expenses.CategoryAccommodation {
		t.Fatalf("first category = %q, want accommodation", got)
	}
	if got := report.ByCategory[0].Percent; got != 58 {
		t.Fatalf("accommodation percent = %d, want 58", got)
	}
	var categorySum core.Money
	for _, line := range report.ByCategory {
		categorySum += line.Total
	}
	if categorySum != report.Total {
		t.Fatalf("categories add up to %d but the trip total is %d", categorySum, report.Total)
	}

	// Creditor first, debtor last — the same order as the balances screen.
	if report.Members[0].DisplayName != "Bob" || report.Members[2].DisplayName != "Cid" {
		t.Fatalf("member order = %q, %q, %q", report.Members[0].DisplayName,
			report.Members[1].DisplayName, report.Members[2].DisplayName)
	}

	byName := map[string]expenses.MemberSummary{}
	for _, m := range report.Members {
		byName[m.DisplayName] = m
	}
	if ann := byName["Ann"]; ann.PaidCount != 2 || ann.ShareCount != 3 || ann.Paid != 15000 || ann.Share != 13000 {
		t.Fatalf("ann = %+v", ann)
	}
	// Cid skipped dinner, so two of the three bills.
	if cid := byName["Cid"]; cid.PaidCount != 0 || cid.ShareCount != 2 || cid.Balance != -10000 {
		t.Fatalf("cid = %+v", cid)
	}

	// The report restates the balances; it must not invent different ones.
	var balanceSum core.Money
	for _, m := range report.Members {
		balanceSum += m.Balance
	}
	if balanceSum != 0 {
		t.Fatalf("balances sum to %d, want 0", balanceSum)
	}
}

func TestBuildReportOnAnEmptyTrip(t *testing.T) {
	report := expenses.BuildReport(nil, nil, "EUR")
	if report.Total != 0 || report.PerPerson != 0 || report.Count != 0 {
		t.Fatalf("empty report = %+v", report)
	}
	// Empty slices, not nil: the client renders them without a null check.
	if report.Expenses == nil || report.ByCategory == nil || report.Members == nil {
		t.Fatalf("empty report has a nil slice: %+v", report)
	}
}

func TestBuildReportIgnoresZeroSharesInTheCount(t *testing.T) {
	ann, bob := core.NewID(), core.NewID()
	list := []expenses.Expense{{
		ID: core.NewID(), Title: "Entry fee", Amount: 5000, Category: expenses.CategoryRegistration,
		PaidBy: ann, SplitType: expenses.SplitCustomAmount,
		Shares: []expenses.Share{{MemberID: ann, Amount: 5000}, {MemberID: bob, Amount: 0}},
	}}
	report := expenses.BuildReport(list, []expenses.Balance{
		{MemberID: ann, DisplayName: "Ann", Paid: 5000, Owed: 5000},
		{MemberID: bob, DisplayName: "Bob"},
	}, "EUR")

	for _, m := range report.Members {
		if m.DisplayName == "Bob" && m.ShareCount != 0 {
			t.Fatalf("bob owes nothing but is counted on %d bills", m.ShareCount)
		}
	}
}
