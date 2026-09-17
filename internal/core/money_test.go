package core

import (
	"math/rand"
	"testing"
)

func TestParseMoney(t *testing.T) {
	cases := []struct {
		in      string
		want    Money
		wantErr bool
	}{
		{in: "120", want: 12000},
		{in: "120.50", want: 12050},
		{in: "120,50", want: 12050},
		{in: "0.05", want: 5},
		{in: "0.5", want: 50},
		{in: ".99", want: 99},
		{in: "-42.05", want: -4205},
		{in: " 1 000 ", want: 100000},
		{in: "", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "1.234", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseMoney(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseMoney(%q) = %v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMoney(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseMoney(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestMoneyFormat(t *testing.T) {
	if got := Money(12050).Format("EUR"); got != "€120.50" {
		t.Errorf("Format(EUR) = %q", got)
	}
	if got := Money(-4205).Format("EUR"); got != "€-42.05" {
		t.Errorf("Format negative = %q", got)
	}
	if got := Money(100).Format("XYZ"); got != "1.00 XYZ" {
		t.Errorf("Format unknown currency = %q", got)
	}
	if got := Money(5).String(); got != "0.05" {
		t.Errorf("String = %q", got)
	}
}

func TestDistributeEquallyIsExact(t *testing.T) {
	cases := []struct {
		total Money
		n     int
		want  []Money
	}{
		{total: 12000, n: 4, want: []Money{3000, 3000, 3000, 3000}},
		{total: 100, n: 3, want: []Money{34, 33, 33}},
		{total: 1, n: 3, want: []Money{1, 0, 0}},
		{total: 0, n: 3, want: []Money{0, 0, 0}},
		{total: -100, n: 3, want: []Money{-34, -33, -33}},
	}
	for _, tc := range cases {
		got := DistributeEqually(tc.total, tc.n)
		if len(got) != len(tc.want) {
			t.Fatalf("DistributeEqually(%d, %d) length = %d", tc.total, tc.n, len(got))
		}
		var sum Money
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("DistributeEqually(%d, %d)[%d] = %d, want %d", tc.total, tc.n, i, got[i], tc.want[i])
			}
			sum += got[i]
		}
		if sum != tc.total {
			t.Errorf("DistributeEqually(%d, %d) sums to %d", tc.total, tc.n, sum)
		}
	}
}

// The invariant that matters: however the cents fall, the shares add back up.
func TestDistributeEquallyAlwaysSumsToTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		total := Money(rng.Int63n(1_000_000))
		n := 1 + rng.Intn(12)
		var sum Money
		for _, share := range DistributeEqually(total, n) {
			sum += share
		}
		if sum != total {
			t.Fatalf("total %d over %d people summed to %d", total, n, sum)
		}
	}
}

func TestDistributeByWeight(t *testing.T) {
	shares, err := DistributeByWeight(10000, []int64{5000, 2500, 2500}) // 50/25/25 %
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []Money{5000, 2500, 2500}
	for i := range want {
		if shares[i] != want[i] {
			t.Errorf("share[%d] = %d, want %d", i, shares[i], want[i])
		}
	}

	// A total that does not divide cleanly still adds up exactly.
	shares, err = DistributeByWeight(1000, []int64{3333, 3333, 3334})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sum Money
	for _, s := range shares {
		sum += s
	}
	if sum != 1000 {
		t.Errorf("weighted shares sum to %d, want 1000", sum)
	}
}

func TestDistributeByWeightRejectsBadInput(t *testing.T) {
	if _, err := DistributeByWeight(100, nil); err == nil {
		t.Error("expected an error for no weights")
	}
	if _, err := DistributeByWeight(100, []int64{0, 0}); err == nil {
		t.Error("expected an error for zero weights")
	}
	if _, err := DistributeByWeight(100, []int64{-1, 2}); err == nil {
		t.Error("expected an error for a negative weight")
	}
}

func TestDistributeByWeightAlwaysSumsToTotal(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		total := Money(rng.Int63n(500_000))
		n := 1 + rng.Intn(8)
		weights := make([]int64, n)
		for j := range weights {
			weights[j] = 1 + rng.Int63n(1000)
		}
		shares, err := DistributeByWeight(total, weights)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var sum Money
		for _, s := range shares {
			sum += s
		}
		if sum != total {
			t.Fatalf("total %d with weights %v summed to %d", total, weights, sum)
		}
	}
}
