// Package decisions is structured group input: a question, a fixed set of
// options, one vote per participant.
//
// Two rules shape the whole module. Voting never decides anything by itself —
// an organiser resolves the decision explicitly, because "the group voted for
// Hotel A" and "we are staying at Hotel A" are different statements. And a
// decision with a deadline closes itself when it passes, so nobody has to
// remember to shut voting down.
package decisions

import (
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// Status is the decision lifecycle.
type Status string

const (
	StatusOpen      Status = "open"      // accepting votes
	StatusClosed    Status = "closed"    // voting finished, awaiting a call
	StatusResolved  Status = "resolved"  // an organiser picked the outcome
	StatusCancelled Status = "cancelled" // abandoned
)

func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusClosed, StatusResolved, StatusCancelled:
		return true
	}
	return false
}

// Option is one choice on the ballot.
type Option struct {
	ID          core.ID `json:"id"          gorm:"primaryKey"`
	DecisionID  core.ID `json:"decision_id" gorm:"not null;index:idx_decision_options,priority:1"`
	Label       string  `json:"label"       gorm:"size:120;not null"`
	Description string  `json:"description,omitempty" gorm:"size:500;not null;default:''"`
	Position    int     `json:"position" gorm:"not null;default:0;index:idx_decision_options,priority:2"`

	Votes  int     `json:"votes"  gorm:"-"`
	Voters []Voter `json:"voters" gorm:"-"`
}

func (Option) TableName() string { return "decision_options" }

// voteRow is one cast ballot. The composite primary key is what enforces "one
// vote per participant"; there is no application check that could be forgotten.
type voteRow struct {
	DecisionID core.ID   `gorm:"primaryKey"`
	MemberID   core.ID   `gorm:"primaryKey"`
	OptionID   core.ID   `gorm:"not null;index"`
	CreatedAt  time.Time `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

func (voteRow) TableName() string { return "decision_votes" }

// Voter is who picked an option. Voting is not anonymous in the MVP: in a
// group of friends, knowing who wants what is the point.
type Voter struct {
	MemberID    core.ID `json:"member_id"`
	DisplayName string  `json:"display_name"`
}

// Decision is the question itself.
type Decision struct {
	ID          core.ID    `json:"id"      gorm:"primaryKey"`
	TripID      core.ID    `json:"trip_id" gorm:"not null;index:idx_decisions_trip,priority:1"`
	Title       string     `json:"title"                 gorm:"size:160;not null"`
	Description string     `json:"description,omitempty" gorm:"size:2000;not null;default:''"`
	Deadline    *time.Time `json:"deadline,omitempty"    gorm:"index"`
	Status      Status     `json:"status" gorm:"size:16;not null;default:'open';index:idx_decisions_trip,priority:2"`

	ResolvedOptionID core.ID    `json:"resolved_option_id,omitempty"`
	ResolutionNote   string     `json:"resolution_note,omitempty" gorm:"size:500;not null;default:''"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`

	CreatedBy core.ID   `json:"created_by" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null"`

	Options []Option `json:"options" gorm:"-"`
	// MyVote is the calling member's choice, zero when they have not voted.
	MyVote core.ID `json:"my_vote,omitempty" gorm:"-"`
	// Eligible is the number of active members; TotalVotes how many answered.
	Eligible   int `json:"eligible"    gorm:"-"`
	TotalVotes int `json:"total_votes" gorm:"-"`
	// Pending lists members who have not voted yet, which is exactly what the
	// attention engine and the reminder need.
	Pending []Voter `json:"pending" gorm:"-"`
}

func (Decision) TableName() string { return "decisions" }

// Open reports whether votes are still accepted.
func (d Decision) Open() bool { return d.Status == StatusOpen }

// Leading returns the option with the most votes and whether it is a sole
// leader. A tie is reported as not decisive so the UI never implies a winner
// that is not there.
func (d Decision) Leading() (Option, bool) {
	var best Option
	bestCount, ties := -1, 0
	for _, o := range d.Options {
		switch {
		case o.Votes > bestCount:
			best, bestCount, ties = o, o.Votes, 1
		case o.Votes == bestCount:
			ties++
		}
	}
	return best, bestCount > 0 && ties == 1
}

// ResolvedOption returns the chosen option, if any.
func (d Decision) ResolvedOption() (Option, bool) {
	if d.ResolvedOptionID.IsZero() {
		return Option{}, false
	}
	for _, o := range d.Options {
		if o.ID == d.ResolvedOptionID {
			return o, true
		}
	}
	return Option{}, false
}

// DeadlineIn reports how long is left, and false when there is no deadline.
func (d Decision) DeadlineIn(now time.Time) (time.Duration, bool) {
	if d.Deadline == nil {
		return 0, false
	}
	return d.Deadline.Sub(now), true
}

// Models lists the tables this module owns, for the migration runner.
func Models() []any { return []any{&Decision{}, &Option{}, &voteRow{}} }
