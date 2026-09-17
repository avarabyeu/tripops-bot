package expenses

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// balancesOf builds a balance set from name/amount pairs, using deterministic
// ids so the expected output of a test is stable.
func balancesOf(t *testing.T, pairs ...any) []Balance {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("balancesOf needs name/amount pairs")
	}
	out := make([]Balance, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		name := pairs[i].(string)
		amount := core.Money(pairs[i+1].(int))
		out = append(out, Balance{
			MemberID:    idFor(name),
			DisplayName: name,
			Amount:      amount,
		})
	}
	return out
}

// idFor maps a name onto a fixed uuid so tests are reproducible.
func idFor(name string) core.ID {
	var id core.ID
	copy(id[:], fmt.Sprintf("%-16s", name))
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func describe(transfers []Transfer) []string {
	out := make([]string, 0, len(transfers))
	for _, t := range transfers {
		out = append(out, fmt.Sprintf("%s owes %s %s", t.FromName, t.ToName, t.Amount.Format(t.Currency)))
	}
	sort.Strings(out)
	return out
}

func sumTransfers(transfers []Transfer) map[core.ID]core.Money {
	net := map[core.ID]core.Money{}
	for _, t := range transfers {
		net[t.From] += t.Amount
		net[t.To] -= t.Amount
	}
	return net
}

// checkClears asserts that applying the transfers brings everyone to zero,
// which is the only correctness property that actually matters.
func checkClears(t *testing.T, balances []Balance, transfers []Transfer) {
	t.Helper()
	net := sumTransfers(transfers)
	for _, b := range balances {
		if got := b.Amount + net[b.MemberID]; got != 0 {
			t.Errorf("%s ends at %s instead of zero", b.DisplayName, got)
		}
	}
	for _, tr := range transfers {
		if tr.Amount <= 0 {
			t.Errorf("transfer of %s is not positive", tr.Amount)
		}
		if tr.From == tr.To {
			t.Error("a transfer must have two different parties")
		}
	}
}

// The worked example from the product spec.
func TestMinimalTransfersSpecExample(t *testing.T) {
	balances := balancesOf(t,
		"Vasya", 9600,
		"Misha", 1700,
		"AV", -4200,
		"Sasha", -4100,
		"Peter", -1300,
		"Kolya", -1700,
	)
	transfers, err := MinimalTransfers(balances, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkClears(t, balances, transfers)

	// Five people are out of pocket and two are owed: four transfers is the
	// best achievable, and Kolya/Misha match exactly.
	if len(transfers) > 5 {
		t.Errorf("produced %d transfers: %v", len(transfers), describe(transfers))
	}
	found := false
	for _, tr := range transfers {
		if tr.FromName == "Kolya" && tr.ToName == "Misha" && tr.Amount == 1700 {
			found = true
		}
	}
	if !found {
		t.Errorf("an exact match should be paid directly, got %v", describe(transfers))
	}
}

func TestMinimalTransfersExactPairsCollapse(t *testing.T) {
	balances := balancesOf(t,
		"A", -1000,
		"B", 1000,
		"C", -2500,
		"D", 2500,
	)
	transfers, err := MinimalTransfers(balances, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkClears(t, balances, transfers)
	if len(transfers) != 2 {
		t.Errorf("two exact pairs should need two transfers, got %v", describe(transfers))
	}
}

func TestMinimalTransfersNothingToSettle(t *testing.T) {
	transfers, err := MinimalTransfers(balancesOf(t, "A", 0, "B", 0), "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(transfers) != 0 {
		t.Errorf("expected no transfers, got %v", describe(transfers))
	}
	if transfers == nil {
		t.Error("an empty result must still be an empty slice, not nil")
	}
}

func TestMinimalTransfersOneDebtorManyCreditors(t *testing.T) {
	balances := balancesOf(t, "Payer", -9000, "A", 3000, "B", 3000, "C", 3000)
	transfers, err := MinimalTransfers(balances, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checkClears(t, balances, transfers)
	if len(transfers) != 3 {
		t.Errorf("expected three transfers, got %v", describe(transfers))
	}
}

// Balances that do not sum to zero mean a bug upstream, and are reported
// rather than quietly absorbed into somebody's debt.
func TestMinimalTransfersRejectsUnbalancedInput(t *testing.T) {
	_, err := MinimalTransfers(balancesOf(t, "A", -100, "B", 50), "EUR")
	if err == nil {
		t.Fatal("expected unbalanced input to be rejected")
	}
	if core.CodeOf(err) != core.CodeInternal {
		t.Errorf("code = %s, want internal", core.CodeOf(err))
	}
}

func TestMinimalTransfersIsDeterministic(t *testing.T) {
	balances := balancesOf(t, "A", -3300, "B", -3300, "C", 2200, "D", 4400)
	first, err := MinimalTransfers(balances, "EUR")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := MinimalTransfers(balances, "EUR")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(again) != len(first) {
			t.Fatalf("run %d produced %d transfers, first produced %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("run %d differs at %d: %+v vs %+v", i, j, again[j], first[j])
			}
		}
	}
}

// The headline guarantee: with n people holding a non-zero balance, never more
// than n-1 transfers, and the balances always clear.
func TestMinimalTransfersProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(2024))
	for iteration := 0; iteration < 2000; iteration++ {
		n := 2 + rng.Intn(8)
		balances := make([]Balance, n)
		var running core.Money
		for i := 0; i < n-1; i++ {
			amount := core.Money(rng.Int63n(200_000) - 100_000)
			balances[i] = Balance{
				MemberID:    idFor(fmt.Sprintf("m%02d", i)),
				DisplayName: fmt.Sprintf("m%02d", i),
				Amount:      amount,
			}
			running += amount
		}
		balances[n-1] = Balance{
			MemberID:    idFor(fmt.Sprintf("m%02d", n-1)),
			DisplayName: fmt.Sprintf("m%02d", n-1),
			Amount:      -running,
		}

		transfers, err := MinimalTransfers(balances, "EUR")
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		checkClears(t, balances, transfers)

		nonZero := 0
		for _, b := range balances {
			if b.Amount != 0 {
				nonZero++
			}
		}
		if nonZero > 0 && len(transfers) > nonZero-1 {
			t.Fatalf("iteration %d: %d transfers for %d non-zero balances",
				iteration, len(transfers), nonZero)
		}
	}
}

func TestSortBalancesPutsCreditorsFirst(t *testing.T) {
	balances := balancesOf(t, "Debtor", -500, "Creditor", 900, "Even", 0)
	SortBalances(balances)
	if balances[0].DisplayName != "Creditor" || balances[2].DisplayName != "Debtor" {
		t.Errorf("unexpected order: %s, %s, %s",
			balances[0].DisplayName, balances[1].DisplayName, balances[2].DisplayName)
	}
}
