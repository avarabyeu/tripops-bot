package decisions

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

// CreateInput is a new question with its ballot.
type CreateInput struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Options     []string   `json:"options"`
	Deadline    *time.Time `json:"deadline"`
}

func (s *Service) Create(ctx context.Context, access trips.Access, in CreateInput) (Decision, error) {
	if err := access.RequireManage(); err != nil {
		return Decision{}, err
	}
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Deadline = core.UTCPtr(in.Deadline)

	labels := make([]string, 0, len(in.Options))
	seen := map[string]bool{}
	for _, o := range in.Options {
		o = strings.TrimSpace(o)
		if o == "" || seen[strings.ToLower(o)] {
			continue
		}
		seen[strings.ToLower(o)] = true
		labels = append(labels, o)
	}

	v := core.NewValidator()
	v.Required(in.Title, "title")
	v.Length(in.Title, "title", 1, 160)
	v.Length(in.Description, "description", 0, 2000)
	v.Check(len(labels) >= 2, "options", "needs at least two distinct choices")
	v.Check(len(labels) <= 10, "options", "supports at most ten choices")
	for _, l := range labels {
		v.Length(l, "options", 1, 120)
	}
	if in.Deadline != nil {
		v.Check(in.Deadline.After(time.Now()), "deadline", "must be in the future")
	}
	if err := v.Err(); err != nil {
		return Decision{}, err
	}

	decision := Decision{
		ID:          core.NewID(),
		TripID:      access.Trip.ID,
		Title:       in.Title,
		Description: in.Description,
		Deadline:    in.Deadline,
		Status:      StatusOpen,
		CreatedBy:   access.User.ID,
	}
	options := make([]Option, len(labels))
	for i, l := range labels {
		options[i] = Option{ID: core.NewID(), DecisionID: decision.ID, Label: l, Position: i}
	}

	err := s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.Insert(ctx, decision)
		if err != nil {
			return err
		}
		decision = created
		return repo.InsertOptions(ctx, decision.ID, options)
	})
	if err != nil {
		return Decision{}, err
	}

	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindDecisionCreated,
		Message: fmt.Sprintf("%s asked: %s", access.Member.DisplayName, decision.Title),
		Meta:    map[string]any{"decision_id": decision.ID.String()},
	})

	body := decision.Title
	if decision.Deadline != nil {
		body += fmt.Sprintf("\nVoting closes %s",
			decision.Deadline.In(access.Trip.Location()).Format("2 Jan at 15:04"))
	}
	s.fanOut(ctx, access.Trip.ID, access.User.ID, notify.Notification{
		TripID:   access.Trip.ID,
		Category: notify.CategoryDecisions,
		Title:    "🗳 " + access.Trip.Title,
		Body:     body,
		Payload:  map[string]any{"decision_id": decision.ID.String()},
	})
	return s.Get(ctx, access, decision.ID)
}

// List returns every decision of the trip with its tally.
func (s *Service) List(ctx context.Context, access trips.Access) ([]Decision, error) {
	list, err := s.repo.ListByTrip(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	return s.hydrate(ctx, access, list)
}

// Get returns one decision with its tally.
func (s *Service) Get(ctx context.Context, access trips.Access, id core.ID) (Decision, error) {
	decision, err := s.load(ctx, access.Trip.ID, id)
	if err != nil {
		return Decision{}, err
	}
	hydrated, err := s.hydrate(ctx, access, []Decision{decision})
	if err != nil {
		return Decision{}, err
	}
	return hydrated[0], nil
}

// Vote records the calling member's choice. Any active member may vote; an
// organiser cannot vote on someone's behalf, because a vote is an opinion.
func (s *Service) Vote(ctx context.Context, access trips.Access, decisionID, optionID core.ID) (Decision, error) {
	if err := access.RequireWrite(); err != nil {
		return Decision{}, err
	}
	decision, err := s.load(ctx, access.Trip.ID, decisionID)
	if err != nil {
		return Decision{}, err
	}
	if !decision.Open() {
		return Decision{}, core.Conflict("voting on this decision is closed")
	}
	if decision.Deadline != nil && time.Now().After(*decision.Deadline) {
		return Decision{}, core.Conflict("the voting deadline has passed")
	}
	belongs, err := s.repo.OptionBelongsTo(ctx, decisionID, optionID)
	if err != nil {
		return Decision{}, err
	}
	if !belongs {
		return Decision{}, core.Invalid("that option is not on this ballot")
	}
	if err := s.repo.CastVote(ctx, decisionID, access.Member.ID, optionID); err != nil {
		return Decision{}, err
	}

	result, err := s.Get(ctx, access, decisionID)
	if err != nil {
		return Decision{}, err
	}
	label := ""
	for _, o := range result.Options {
		if o.ID == optionID {
			label = o.Label
		}
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindDecisionVoted,
		Message: fmt.Sprintf("%s voted for %s", access.Member.DisplayName, label),
		Meta:    map[string]any{"decision_id": decisionID.String()},
	})
	return result, nil
}

// Close stops voting without picking an outcome.
func (s *Service) Close(ctx context.Context, access trips.Access, decisionID core.ID) (Decision, error) {
	if err := access.RequireManage(); err != nil {
		return Decision{}, err
	}
	decision, err := s.load(ctx, access.Trip.ID, decisionID)
	if err != nil {
		return Decision{}, err
	}
	if decision.Status != StatusOpen {
		return Decision{}, core.Conflict("this decision is already %s", decision.Status)
	}
	if _, err := s.repo.SetStatus(ctx, decisionID, StatusClosed, core.Nil, "", time.Now().UTC()); err != nil {
		return Decision{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindDecisionClosed,
		Message: fmt.Sprintf("%s closed voting on %q", access.Member.DisplayName, decision.Title),
	})
	return s.Get(ctx, access, decisionID)
}

// Cancel abandons a decision.
func (s *Service) Cancel(ctx context.Context, access trips.Access, decisionID core.ID) (Decision, error) {
	if err := access.RequireManage(); err != nil {
		return Decision{}, err
	}
	if _, err := s.load(ctx, access.Trip.ID, decisionID); err != nil {
		return Decision{}, err
	}
	if _, err := s.repo.SetStatus(ctx, decisionID, StatusCancelled, core.Nil, "", time.Now().UTC()); err != nil {
		return Decision{}, err
	}
	return s.Get(ctx, access, decisionID)
}

// ResolveInput picks the outcome. The winning option is not enforced
// automatically anywhere in the product: an organiser states the call.
type ResolveInput struct {
	OptionID core.ID `json:"option_id"`
	Note     string  `json:"note"`
}

func (s *Service) Resolve(ctx context.Context, access trips.Access, decisionID core.ID, in ResolveInput) (Decision, error) {
	if err := access.RequireManage(); err != nil {
		return Decision{}, err
	}
	decision, err := s.load(ctx, access.Trip.ID, decisionID)
	if err != nil {
		return Decision{}, err
	}
	if decision.Status == StatusCancelled {
		return Decision{}, core.Conflict("this decision was cancelled")
	}
	if in.OptionID.IsZero() {
		return Decision{}, core.Invalid("option_id is required to resolve a decision")
	}
	belongs, err := s.repo.OptionBelongsTo(ctx, decisionID, in.OptionID)
	if err != nil {
		return Decision{}, err
	}
	if !belongs {
		return Decision{}, core.Invalid("that option is not on this ballot")
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 500 {
		return Decision{}, core.Invalid("note must be at most 500 characters")
	}
	if _, err := s.repo.SetStatus(ctx, decisionID, StatusResolved, in.OptionID, note, time.Now().UTC()); err != nil {
		return Decision{}, err
	}

	result, err := s.Get(ctx, access, decisionID)
	if err != nil {
		return Decision{}, err
	}
	chosen, _ := result.ResolvedOption()
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindDecisionResolved,
		Message: fmt.Sprintf("%s decided: %s — %s", access.Member.DisplayName, decision.Title, chosen.Label),
		Meta:    map[string]any{"decision_id": decisionID.String()},
	})
	s.fanOut(ctx, access.Trip.ID, access.User.ID, notify.Notification{
		TripID:   access.Trip.ID,
		Category: notify.CategoryDecisions,
		Title:    "✅ " + decision.Title,
		Body:     "Decided: " + chosen.Label,
		Payload:  map[string]any{"decision_id": decisionID.String()},
	})
	return result, nil
}

// AutoCloseDue closes every open decision whose deadline has passed and tells
// the group. Called by the scheduler; safe to run as often as you like.
func (s *Service) AutoCloseDue(ctx context.Context, now time.Time) (int, error) {
	due, err := s.repo.DueForAutoClose(ctx, now, 100)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, d := range due {
		if _, err := s.repo.SetStatus(ctx, d.ID, StatusClosed, core.Nil, "", now); err != nil {
			return closed, err
		}
		closed++

		s.activity.Log(ctx, activity.Entry{
			TripID: d.TripID, ActorName: "TripOps", Kind: activity.KindDecisionClosed,
			Message: fmt.Sprintf("Voting closed on %q", d.Title),
		})
		s.fanOut(ctx, d.TripID, core.Nil, notify.Notification{
			TripID:      d.TripID,
			Category:    notify.CategoryDecisions,
			Title:       "🗳 Voting closed",
			Body:        d.Title + "\nThe organiser can now confirm the outcome.",
			DedupeKey:   notify.DedupeKey("decision_closed", d.ID.String()),
			Payload:     map[string]any{"decision_id": d.ID.String()},
			ScheduledAt: now,
		})
	}
	return closed, nil
}

// ------------------------------------------------------------------ helpers --

func (s *Service) load(ctx context.Context, tripID, id core.ID) (Decision, error) {
	d, err := s.repo.ByID(ctx, id)
	if err != nil {
		return Decision{}, err
	}
	if d.TripID != tripID {
		return Decision{}, core.NotFound("decision")
	}
	return d, nil
}

// hydrate attaches options, votes and the "who still owes us an answer" list.
func (s *Service) hydrate(ctx context.Context, access trips.Access, list []Decision) ([]Decision, error) {
	if len(list) == 0 {
		return []Decision{}, nil
	}
	options, err := s.repo.Options(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	votes, err := s.repo.Votes(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	members, err := s.trips.Members(ctx, access)
	if err != nil {
		return nil, err
	}
	active := make([]trips.Member, 0, len(members))
	for _, m := range members {
		if m.Active() {
			active = append(active, m)
		}
	}

	votesByDecision := map[core.ID][]Vote{}
	for _, v := range votes {
		votesByDecision[v.DecisionID] = append(votesByDecision[v.DecisionID], v)
	}

	for i := range list {
		d := &list[i]
		d.Options = append([]Option{}, options[d.ID]...)
		d.Eligible = len(active)

		byOption := map[core.ID][]Voter{}
		voted := map[core.ID]bool{}
		for _, v := range votesByDecision[d.ID] {
			byOption[v.OptionID] = append(byOption[v.OptionID], Voter{MemberID: v.MemberID, DisplayName: v.DisplayName})
			voted[v.MemberID] = true
			if v.MemberID == access.Member.ID {
				d.MyVote = v.OptionID
			}
		}
		d.TotalVotes = len(voted)
		for j := range d.Options {
			voters := byOption[d.Options[j].ID]
			if voters == nil {
				voters = []Voter{}
			}
			d.Options[j].Voters = voters
			d.Options[j].Votes = len(voters)
		}
		d.Pending = []Voter{}
		for _, m := range active {
			if !voted[m.ID] {
				d.Pending = append(d.Pending, Voter{MemberID: m.ID, DisplayName: m.DisplayName})
			}
		}
	}
	return list, nil
}

func (s *Service) fanOut(ctx context.Context, tripID, exceptUserID core.ID, template notify.Notification) {
	userIDs, err := s.trips.ActiveMemberUserIDs(ctx, tripID)
	if err != nil {
		return
	}
	items := make([]notify.Notification, 0, len(userIDs))
	for _, id := range userIDs {
		if id == exceptUserID {
			continue
		}
		n := template
		n.UserID = id
		// A shared dedupe key has to become per-recipient, or only the first
		// member of the group would ever be told.
		if template.DedupeKey != nil {
			n.DedupeKey = notify.DedupeKey(*template.DedupeKey, id.String())
		}
		items = append(items, n)
	}
	_ = s.notify.Enqueue(ctx, items...)
}
