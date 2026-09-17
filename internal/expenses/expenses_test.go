package expenses

import (
	"math/rand"
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func members(n int) []core.ID {
	out := make([]core.ID, n)
	for i := range out {
		out[i] = core.NewID()
	}
	return out
}

func participants(ids []core.ID, weights ...int64) []Participant {
	out := make([]Participant, len(ids))
	for i, id := range ids {
		out[i] = Participant{MemberID: id}
		if i < len(weights) {
			out[i].Weight = weights[i]
		}
	}
	return out
}

func sumShares(shares []Share) core.Money {
	var sum core.Money
	for _, s := range shares {
		sum += s.Amount
	}
	return sum
}

func TestComputeSharesEqual(t *testing.T) {
	ids := members(4)
	shares, err := ComputeShares(12000, SplitEqual, participants(ids))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range shares {
		if s.Amount != 3000 {
			t.Errorf("share = %d, want 3000", s.Amount)
		}
	}
	if sumShares(shares) != 12000 {
		t.Errorf("shares sum to %d", sumShares(shares))
	}
}

// The classic case: €10 between three people is not €3.33 each.
func TestComputeSharesEqualHandlesRemainder(t *testing.T) {
	ids := members(3)
	shares, err := ComputeShares(1000, SplitEqual, participants(ids))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shares[0].Amount != 334 || shares[1].Amount != 333 || shares[2].Amount != 333 {
		t.Errorf("shares = %d, %d, %d; want 334, 333, 333",
			shares[0].Amount, shares[1].Amount, shares[2].Amount)
	}
	if sumShares(shares) != 1000 {
		t.Errorf("shares sum to %d, want 1000", sumShares(shares))
	}
}

func TestComputeSharesCustomAmount(t *testing.T) {
	ids := members(3)
	shares, err := ComputeShares(10000, SplitCustomAmount, participants(ids, 5000, 3000, 2000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []core.Money{5000, 3000, 2000}
	for i, s := range shares {
		if s.Amount != want[i] {
			t.Errorf("share[%d] = %d, want %d", i, s.Amount, want[i])
		}
		if s.Weight == nil || *s.Weight != int64(want[i]) {
			t.Errorf("share[%d] should remember its input weight", i)
		}
	}
}

func TestComputeSharesCustomAmountMustAddUp(t *testing.T) {
	ids := members(2)
	_, err := ComputeShares(10000, SplitCustomAmount, participants(ids, 5000, 4000))
	if err == nil {
		t.Fatal("expected shares that do not add up to be rejected")
	}
	if core.CodeOf(err) != core.CodeInvalid {
		t.Errorf("code = %s, want invalid", core.CodeOf(err))
	}
}

func TestComputeSharesPercentage(t *testing.T) {
	ids := members(3)
	// 50% / 30% / 20% in basis points.
	shares, err := ComputeShares(10001, SplitPercentage, participants(ids, 5000, 3000, 2000))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sumShares(shares) != 10001 {
		t.Errorf("percentage shares sum to %d, want 10001", sumShares(shares))
	}
	if shares[0].Amount < shares[1].Amount || shares[1].Amount < shares[2].Amount {
		t.Errorf("shares are not ordered by weight: %v", shares)
	}
}

func TestComputeSharesPercentageMustBe100(t *testing.T) {
	ids := members(2)
	if _, err := ComputeShares(10000, SplitPercentage, participants(ids, 5000, 4000)); err == nil {
		t.Fatal("expected percentages that do not total 100% to be rejected")
	}
}

func TestComputeSharesRejectsBadInput(t *testing.T) {
	ids := members(2)
	if _, err := ComputeShares(0, SplitEqual, participants(ids)); err == nil {
		t.Error("a zero amount must be rejected")
	}
	if _, err := ComputeShares(100, SplitEqual, nil); err == nil {
		t.Error("an expense with no participants must be rejected")
	}
	dup := []Participant{{MemberID: ids[0]}, {MemberID: ids[0]}}
	if _, err := ComputeShares(100, SplitEqual, dup); err == nil {
		t.Error("a duplicated participant must be rejected")
	}
	if _, err := ComputeShares(100, SplitType("raffle"), participants(ids)); err == nil {
		t.Error("an unknown split type must be rejected")
	}
}

// Whatever the split, the shares reconstruct the total exactly. Balances are
// only trustworthy because of this.
func TestSharesAlwaysReconstructTheTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000; i++ {
		n := 1 + rng.Intn(8)
		ids := members(n)
		total := core.Money(1 + rng.Int63n(500_000))

		shares, err := ComputeShares(total, SplitEqual, participants(ids))
		if err != nil || sumShares(shares) != total {
			t.Fatalf("equal split of %d over %d: sum %d err %v", total, n, sumShares(shares), err)
		}

		// Percentage weights that add to exactly 100%.
		weights := make([]int64, n)
		remaining := BasisPoints
		for j := 0; j < n-1; j++ {
			if remaining <= 1 {
				break
			}
			weights[j] = rng.Int63n(remaining/int64(n-j)) + 1
			remaining -= weights[j]
		}
		weights[n-1] = remaining
		shares, err = ComputeShares(total, SplitPercentage, participants(ids, weights...))
		if err != nil || sumShares(shares) != total {
			t.Fatalf("percentage split of %d with %v: sum %d err %v", total, weights, sumShares(shares), err)
		}
	}
}

func TestSummariseTotals(t *testing.T) {
	list := []Expense{
		{Amount: 12000, Category: CategoryFuel},
		{Amount: 4000, Category: CategoryFood},
		{Amount: 2000, Category: CategoryFuel},
	}
	totals := SummariseTotals(list, "EUR")
	if totals.Total != 18000 {
		t.Errorf("total = %d, want 18000", totals.Total)
	}
	if totals.ByCategory[CategoryFuel] != 14000 {
		t.Errorf("fuel = %d, want 14000", totals.ByCategory[CategoryFuel])
	}
	if totals.Count != 3 {
		t.Errorf("count = %d", totals.Count)
	}
}

func TestCategoryAndSplitValidation(t *testing.T) {
	if !CategoryFuel.Valid() || Category("bribes").Valid() {
		t.Error("category validation is wrong")
	}
	if !SplitEqual.Valid() || SplitType("dice").Valid() {
		t.Error("split type validation is wrong")
	}
}
