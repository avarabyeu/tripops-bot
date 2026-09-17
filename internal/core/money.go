package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Money is an amount in minor units (cents) of a currency. Trips are
// single-currency in the MVP, so the currency travels alongside the amount
// rather than inside it and is validated at the trip boundary.
//
// Everything financial in this codebase is integer arithmetic. Floating point
// never touches an amount, which is why splitting uses a largest remainder
// distribution instead of rounding each share independently.
type Money int64

func (m Money) Int64() int64 { return int64(m) }

func (m Money) Abs() Money {
	if m < 0 {
		return -m
	}
	return m
}

// String renders the amount with two decimals, e.g. "-42.05".
func (m Money) String() string {
	sign := ""
	v := int64(m)
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// Format renders the amount with its currency symbol or code, e.g. "€42.05".
func (m Money) Format(currency string) string {
	if sym, ok := currencySymbols[strings.ToUpper(currency)]; ok {
		return sym + m.String()
	}
	return m.String() + " " + strings.ToUpper(currency)
}

var currencySymbols = map[string]string{
	"EUR": "€",
	"USD": "$",
	"GBP": "£",
	"PLN": "zł",
	"UAH": "₴",
	"CZK": "Kč",
}

// ParseMoney reads a human amount ("120", "120.50", "120,50") into minor units.
func ParseMoney(s string) (Money, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, " ", ""))
	s = strings.ReplaceAll(s, ",", ".")
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	var cents int64
	if hasFrac {
		switch len(frac) {
		case 0:
		case 1:
			frac += "0"
			fallthrough
		case 2:
			cents, err = strconv.ParseInt(frac, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid amount %q", s)
			}
		default:
			return 0, fmt.Errorf("amount %q has more than two decimals", s)
		}
	}
	total := w*100 + cents
	if neg {
		total = -total
	}
	return Money(total), nil
}

// IsSupportedCurrency guards the small set the MVP formats properly.
func IsSupportedCurrency(code string) bool {
	_, ok := currencySymbols[strings.ToUpper(code)]
	return ok
}

// SupportedCurrencies lists the accepted ISO codes in a stable order.
func SupportedCurrencies() []string {
	out := make([]string, 0, len(currencySymbols))
	for code := range currencySymbols {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// DistributeEqually splits total across n shares so that the shares sum
// exactly back to total. The remaining cents are handed out one by one to the
// first shares, which is the standard "largest remainder" behaviour for an
// equal split and keeps the result deterministic.
func DistributeEqually(total Money, n int) []Money {
	if n <= 0 {
		return nil
	}
	shares := make([]Money, n)
	base := int64(total) / int64(n)
	rem := int64(total) % int64(n)
	step := int64(1)
	if rem < 0 {
		rem, step = -rem, -1
	}
	for i := range shares {
		shares[i] = Money(base)
		if int64(i) < rem {
			shares[i] += Money(step)
		}
	}
	return shares
}

// DistributeByWeight splits total proportionally to weights (basis points,
// percentages, or any positive units) and gives the rounding remainder to the
// largest fractional parts, breaking ties by index so the result is stable.
func DistributeByWeight(total Money, weights []int64) ([]Money, error) {
	if len(weights) == 0 {
		return nil, fmt.Errorf("no weights")
	}
	var sum int64
	for _, w := range weights {
		if w < 0 {
			return nil, fmt.Errorf("negative weight")
		}
		sum += w
	}
	if sum == 0 {
		return nil, fmt.Errorf("weights sum to zero")
	}

	type rest struct {
		idx int
		rem int64
	}
	shares := make([]Money, len(weights))
	rests := make([]rest, len(weights))
	var allocated int64
	for i, w := range weights {
		num := int64(total) * w
		q := num / sum
		shares[i] = Money(q)
		allocated += q
		rests[i] = rest{idx: i, rem: num - q*sum}
	}
	leftover := int64(total) - allocated
	step := int64(1)
	if leftover < 0 {
		leftover, step = -leftover, -1
	}
	sort.SliceStable(rests, func(a, b int) bool {
		if rests[a].rem != rests[b].rem {
			return rests[a].rem > rests[b].rem
		}
		return rests[a].idx < rests[b].idx
	})
	for i := int64(0); i < leftover; i++ {
		shares[rests[i%int64(len(rests))].idx] += Money(step)
	}
	return shares, nil
}
