package decisions

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func option(label string, votes int) Option {
	o := Option{ID: core.NewID(), Label: label, Votes: votes}
	for i := 0; i < votes; i++ {
		o.Voters = append(o.Voters, Voter{MemberID: core.NewID(), DisplayName: "voter"})
	}
	return o
}

func TestLeadingOption(t *testing.T) {
	d := Decision{Options: []Option{option("Hotel A", 3), option("Apartment B", 1), option("Hotel C", 0)}}
	best, decisive := d.Leading()
	if !decisive {
		t.Fatal("a clear winner should be decisive")
	}
	if best.Label != "Hotel A" {
		t.Errorf("leading = %q, want Hotel A", best.Label)
	}
}

// A tie is reported as not decisive: the UI must never imply a winner that the
// group did not produce.
func TestLeadingOptionTieIsNotDecisive(t *testing.T) {
	d := Decision{Options: []Option{option("A", 2), option("B", 2)}}
	if _, decisive := d.Leading(); decisive {
		t.Error("a tie must not be decisive")
	}
}

func TestLeadingOptionWithNoVotes(t *testing.T) {
	d := Decision{Options: []Option{option("A", 0), option("B", 0)}}
	if _, decisive := d.Leading(); decisive {
		t.Error("an unvoted decision has no leader")
	}
}

func TestResolvedOption(t *testing.T) {
	chosen := option("Apartment B", 1)
	d := Decision{Options: []Option{option("Hotel A", 3), chosen}, ResolvedOptionID: chosen.ID}

	// Deliberately resolving against the vote count is allowed: the organiser
	// decides, the vote advises.
	got, ok := d.ResolvedOption()
	if !ok || got.Label != "Apartment B" {
		t.Errorf("ResolvedOption = %+v, ok=%v", got, ok)
	}

	if _, ok := (Decision{}).ResolvedOption(); ok {
		t.Error("an unresolved decision has no chosen option")
	}
}

func TestDeadlineIn(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(6 * time.Hour)
	d := Decision{Deadline: &deadline}

	left, ok := d.DeadlineIn(now)
	if !ok || left != 6*time.Hour {
		t.Errorf("DeadlineIn = %v, ok=%v", left, ok)
	}
	if _, ok := (Decision{}).DeadlineIn(now); ok {
		t.Error("a decision with no deadline reports none")
	}
}

func TestStatusValidation(t *testing.T) {
	for _, s := range []Status{StatusOpen, StatusClosed, StatusResolved, StatusCancelled} {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if Status("pondering").Valid() {
		t.Error("unknown statuses must not validate")
	}
	if !(Decision{Status: StatusOpen}).Open() || (Decision{Status: StatusClosed}).Open() {
		t.Error("Open() is wrong")
	}
}
