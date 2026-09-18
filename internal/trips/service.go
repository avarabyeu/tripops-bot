package trips

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// InvitePrefix is the deep link payload prefix: https://t.me/bot?start=inv_<token>.
const InvitePrefix = "inv_"

// Service implements the trip and membership use cases.
type Service struct {
	database *db.DB
	repo     *Repo
	users    *users.Service
	activity activity.Logger
	notify   notify.Enqueuer
	// deepLink renders an invite payload as a t.me URL. Injected because it
	// depends on the bot username, which is configuration, not domain.
	deepLink func(payload string) string
}

func NewService(
	database *db.DB,
	usersSvc *users.Service,
	act activity.Logger,
	notifier notify.Enqueuer,
	deepLink func(string) string,
) *Service {
	if deepLink == nil {
		deepLink = func(string) string { return "" }
	}
	return &Service{
		database: database,
		repo:     NewRepo(database.DB),
		users:    usersSvc,
		activity: act,
		notify:   notifier,
		deepLink: deepLink,
	}
}

// CreateInput is a new trip. Dates are calendar dates in the trip timezone.
type CreateInput struct {
	Title       string    `json:"title"`
	Description string    `json:"description"`
	StartDate   core.Date `json:"start_date"`
	EndDate     core.Date `json:"end_date"`
	Timezone    string    `json:"timezone"`
	Currency    string    `json:"currency"`
}

func (in *CreateInput) normalise() {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.Timezone = strings.TrimSpace(in.Timezone)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if in.Currency == "" {
		in.Currency = "EUR"
	}
	if in.EndDate.IsZero() {
		in.EndDate = in.StartDate
	}
}

func (in CreateInput) validate() error {
	v := core.NewValidator()
	v.Required(in.Title, "title")
	v.Length(in.Title, "title", 1, 120)
	v.Length(in.Description, "description", 0, 2000)
	v.Check(!in.StartDate.IsZero(), "start_date", "is required")
	v.Check(!in.EndDate.IsZero(), "end_date", "is required")
	if !in.StartDate.IsZero() && !in.EndDate.IsZero() {
		v.Check(!in.EndDate.Before(in.StartDate), "end_date", "must not be before the start date")
		v.Check(in.StartDate.DaysUntil(in.EndDate) <= 365, "end_date", "must be within a year of the start date")
	}
	v.Check(core.IsValidTimezone(in.Timezone), "timezone", "is not a known IANA timezone")
	v.Check(core.IsSupportedCurrency(in.Currency), "currency",
		"must be one of: "+strings.Join(core.SupportedCurrencies(), ", "))
	return v.Err()
}

// Create makes the trip and its owner membership in one transaction: a trip
// without an owner row would be unreachable by anyone, including its creator.
func (s *Service) Create(ctx context.Context, owner users.User, in CreateInput) (Trip, error) {
	in.normalise()
	if err := in.validate(); err != nil {
		return Trip{}, err
	}

	trip := Trip{
		ID:          core.NewID(),
		Title:       in.Title,
		Description: in.Description,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		Timezone:    in.Timezone,
		Currency:    in.Currency,
		OwnerID:     owner.ID,
		Status:      core.TripPlanning,
	}

	err := s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		created, err := repo.InsertTrip(ctx, trip)
		if err != nil {
			return err
		}
		trip = created
		now := time.Now().UTC()
		_, err = repo.InsertMember(ctx, Member{
			ID:          core.NewID(),
			TripID:      trip.ID,
			UserID:      owner.ID,
			DisplayName: owner.DisplayName(),
			Role:        core.RoleOwner,
			Status:      core.MemberActive,
			JoinedAt:    &now,
		})
		return err
	})
	if err != nil {
		return Trip{}, err
	}

	s.activity.Log(ctx, activity.Entry{
		TripID: trip.ID, ActorUserID: owner.ID, ActorName: owner.DisplayName(),
		Kind: activity.KindTripCreated, Message: fmt.Sprintf("%s created the trip", owner.DisplayName()),
	})
	return trip, nil
}

// List returns the caller's trips.
func (s *Service) List(ctx context.Context, userID core.ID, includeArchived bool) ([]TripSummary, error) {
	return s.repo.ListTripsForUser(ctx, userID, includeArchived)
}

// Upcoming lists trips that are running or about to, for the scheduler.
func (s *Service) Upcoming(ctx context.Context, today core.Date, horizonDays int) ([]Trip, error) {
	return s.repo.UpcomingTrips(ctx, today, horizonDays)
}

// AdvanceStatuses moves trips between planning, active and completed to match
// their dates, and reports how many it changed.
//
// Nobody is told. Saturday arriving is not news, so there is no notification
// and no activity entry — the status is a derived fact the UI reads, not an
// event. Archived trips are never touched: see Trip.DerivedStatus.
func (s *Service) AdvanceStatuses(ctx context.Context, now time.Time) (int, error) {
	candidates, err := s.repo.TripsForStatusReview(ctx, core.DateOf(now, time.UTC))
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, trip := range candidates {
		derived := trip.DerivedStatus(now)
		if derived == trip.Status {
			continue
		}
		if err := s.repo.SetStatus(ctx, trip.ID, derived); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// Get returns a trip the caller belongs to.
func (s *Service) Get(ctx context.Context, tripID, userID core.ID) (Trip, error) {
	access, err := s.Access(ctx, tripID, userID)
	if err != nil {
		return Trip{}, err
	}
	return access.Trip, nil
}

// UpdateInput patches a trip. Nil fields are left alone.
type UpdateInput struct {
	Title       *string          `json:"title"`
	Description *string          `json:"description"`
	StartDate   *core.Date       `json:"start_date"`
	EndDate     *core.Date       `json:"end_date"`
	Timezone    *string          `json:"timezone"`
	Currency    *string          `json:"currency"`
	Status      *core.TripStatus `json:"status"`
}

// Update applies a patch. Admins may edit the trip; only the owner may archive
// it, because archiving freezes the trip for everyone.
func (s *Service) Update(ctx context.Context, access Access, in UpdateInput) (Trip, error) {
	if err := access.Require(core.RoleAdmin); err != nil {
		return Trip{}, err
	}
	trip := access.Trip
	archiving := in.Status != nil && *in.Status == core.TripArchived
	unarchiving := in.Status != nil && *in.Status != core.TripArchived && trip.Status == core.TripArchived
	if archiving || unarchiving {
		if !access.IsOwner() {
			return Trip{}, core.Forbidden("only the trip owner can archive or restore a trip")
		}
	} else if !trip.Status.Writable() {
		return Trip{}, core.Conflict("this trip is archived and cannot be changed")
	}

	if in.Title != nil {
		trip.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		trip.Description = strings.TrimSpace(*in.Description)
	}
	if in.StartDate != nil {
		trip.StartDate = *in.StartDate
	}
	if in.EndDate != nil {
		trip.EndDate = *in.EndDate
	}
	if in.Timezone != nil {
		trip.Timezone = strings.TrimSpace(*in.Timezone)
	}
	if in.Currency != nil {
		trip.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	if in.Status != nil {
		trip.Status = *in.Status
	}

	v := core.NewValidator()
	v.Required(trip.Title, "title")
	v.Length(trip.Title, "title", 1, 120)
	v.Length(trip.Description, "description", 0, 2000)
	v.Check(!trip.EndDate.Before(trip.StartDate), "end_date", "must not be before the start date")
	v.Check(core.IsValidTimezone(trip.Timezone), "timezone", "is not a known IANA timezone")
	v.Check(core.IsSupportedCurrency(trip.Currency), "currency",
		"must be one of: "+strings.Join(core.SupportedCurrencies(), ", "))
	v.Check(trip.Status.Valid(), "status", "is not a known trip status")
	if err := v.Err(); err != nil {
		return Trip{}, err
	}
	if in.Currency != nil && trip.Currency != access.Trip.Currency {
		// Amounts are stored in minor units of the trip currency with no rate
		// history, so changing it after the fact would silently reinterpret
		// every recorded expense.
		var count int64
		if err := s.database.WithContext(ctx).Table("expenses").
			Where("trip_id = ?", trip.ID).Count(&count).Error; err != nil {
			return Trip{}, core.Internal(fmt.Errorf("trips: count expenses: %w", err))
		}
		if count > 0 {
			return Trip{}, core.Conflict("the currency cannot be changed once expenses have been recorded")
		}
	}

	updated, err := s.repo.UpdateTrip(ctx, trip)
	if err != nil {
		return Trip{}, err
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindTripUpdated,
		Message: fmt.Sprintf("%s updated the trip", access.Member.DisplayName),
	})
	return updated, nil
}

// Delete removes a trip and everything on it: people, timeline, decisions,
// logistics, checklists, expenses and their settlements.
//
// Owner only, and irreversible — there is no soft delete. Archiving exists for
// "we are done with this trip"; deleting is for "this should not have been
// created", so keeping a hidden copy would serve nobody.
func (s *Service) Delete(ctx context.Context, access Access) error {
	if !access.IsOwner() {
		return core.Forbidden("only the trip owner can delete a trip")
	}
	return s.repo.DeleteTrip(ctx, access.Trip.ID)
}

// ------------------------------------------------------------- memberships --

// Members lists the group.
func (s *Service) Members(ctx context.Context, access Access) ([]Member, error) {
	return s.repo.ListMembers(ctx, access.Trip.ID, false)
}

// MemberCounts summarises the group for dashboards.
func (s *Service) MemberCounts(ctx context.Context, tripID core.ID) (MemberCounts, error) {
	return s.repo.MemberCounts(ctx, tripID)
}

// ActiveMemberUserIDs returns the users to notify about trip-wide events.
func (s *Service) ActiveMemberUserIDs(ctx context.Context, tripID core.ID) ([]core.ID, error) {
	return s.repo.ActiveMemberUserIDs(ctx, tripID)
}

// MemberByID loads one member of the trip the caller has access to.
func (s *Service) MemberByID(ctx context.Context, tripID, memberID core.ID) (Member, error) {
	member, err := s.repo.MemberByID(ctx, memberID)
	if err != nil {
		return Member{}, err
	}
	if member.TripID != tripID {
		return Member{}, core.NotFound("member")
	}
	return member, nil
}

// MemberByAnyTrip loads a member row by id without knowing its trip. The bot
// needs it to resolve the trip behind a callback payload before it can check
// access; the caller must still authorize against the returned trip id.
func (s *Service) MemberByAnyTrip(ctx context.Context, memberID core.ID) (Member, error) {
	return s.repo.MemberByID(ctx, memberID)
}

// UpdateMemberInput patches a membership.
type UpdateMemberInput struct {
	DisplayName *string            `json:"display_name"`
	Role        *core.Role         `json:"role"`
	Status      *core.MemberStatus `json:"status"`
}

// UpdateMember changes a display name, role or status.
//
// Rules: anyone may rename themselves; only organisers may touch someone else;
// only the owner may hand out or take away the admin role; the owner row can
// never be demoted, because that would leave the trip ownerless.
func (s *Service) UpdateMember(ctx context.Context, access Access, memberID core.ID, in UpdateMemberInput) (Member, error) {
	if err := access.RequireWrite(); err != nil {
		return Member{}, err
	}
	member, err := s.MemberByID(ctx, access.Trip.ID, memberID)
	if err != nil {
		return Member{}, err
	}
	self := member.ID == access.Member.ID
	if !self && !access.IsManager() {
		return Member{}, core.Forbidden("only organisers can change other participants")
	}

	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		v := core.NewValidator()
		v.Required(name, "display_name")
		v.Length(name, "display_name", 1, 60)
		if err := v.Err(); err != nil {
			return Member{}, err
		}
		member.DisplayName = name
	}

	if in.Role != nil && *in.Role != member.Role {
		if !access.IsOwner() {
			return Member{}, core.Forbidden("only the trip owner can change roles")
		}
		if member.Role == core.RoleOwner {
			return Member{}, core.Conflict("transfer ownership instead of demoting the owner")
		}
		if *in.Role == core.RoleOwner {
			return Member{}, core.Conflict("use ownership transfer to make someone the owner")
		}
		if !in.Role.Valid() {
			return Member{}, core.Invalid("unknown role")
		}
		member.Role = *in.Role
	}

	if in.Status != nil && *in.Status != member.Status {
		if !in.Status.Valid() {
			return Member{}, core.Invalid("unknown membership status")
		}
		if member.Role == core.RoleOwner && *in.Status != core.MemberActive {
			return Member{}, core.Conflict("the owner cannot leave their own trip; transfer ownership first")
		}
		if !self && !access.IsManager() {
			return Member{}, core.Forbidden("only organisers can change participation")
		}
		member.Status = *in.Status
		if *in.Status == core.MemberActive && member.JoinedAt == nil {
			now := time.Now().UTC()
			member.JoinedAt = &now
		}
	}

	updated, err := s.repo.UpdateMember(ctx, member)
	if err != nil {
		return Member{}, err
	}
	if in.Role != nil {
		s.activity.Log(ctx, activity.Entry{
			TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
			ActorName: access.Member.DisplayName, Kind: activity.KindMemberRoleChanged,
			Message: fmt.Sprintf("%s made %s %s", access.Member.DisplayName, updated.DisplayName, updated.Role),
		})
	}
	return updated, nil
}

// RemoveMember takes someone off the trip. Their historic contributions (votes,
// expenses) stay, which is why this is a status change and not a delete.
func (s *Service) RemoveMember(ctx context.Context, access Access, memberID core.ID) error {
	if err := access.RequireWrite(); err != nil {
		return err
	}
	member, err := s.MemberByID(ctx, access.Trip.ID, memberID)
	if err != nil {
		return err
	}
	self := member.ID == access.Member.ID
	if !self && !access.IsManager() {
		return core.Forbidden("only organisers can remove participants")
	}
	if member.Role == core.RoleOwner {
		return core.Conflict("the owner cannot be removed; transfer ownership first")
	}

	member.Status = core.MemberRemoved
	if _, err := s.repo.UpdateMember(ctx, member); err != nil {
		return err
	}
	verb := "removed"
	if self {
		verb = "left the trip"
	}
	msg := fmt.Sprintf("%s removed %s from the trip", access.Member.DisplayName, member.DisplayName)
	if self {
		msg = fmt.Sprintf("%s %s", member.DisplayName, verb)
	}
	s.activity.Log(ctx, activity.Entry{
		TripID: access.Trip.ID, ActorUserID: access.User.ID, ActorMemberID: access.Member.ID,
		ActorName: access.Member.DisplayName, Kind: activity.KindMemberLeft, Message: msg,
	})
	return nil
}

// TransferOwnership moves the owner role to another active member. Both rows
// change in one transaction so the single-owner index is never violated.
func (s *Service) TransferOwnership(ctx context.Context, access Access, toMemberID core.ID) error {
	if !access.IsOwner() {
		return core.Forbidden("only the trip owner can transfer ownership")
	}
	if err := access.RequireWrite(); err != nil {
		return err
	}
	target, err := s.MemberByID(ctx, access.Trip.ID, toMemberID)
	if err != nil {
		return err
	}
	if !target.Active() {
		return core.Conflict("ownership can only be transferred to an active participant")
	}
	if target.ID == access.Member.ID {
		return core.Invalid("you already own this trip")
	}

	return s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		current := access.Member
		current.Role = core.RoleAdmin
		if _, err := repo.UpdateMember(ctx, current); err != nil {
			return err
		}
		target.Role = core.RoleOwner
		if _, err := repo.UpdateMember(ctx, target); err != nil {
			return err
		}
		trip := access.Trip
		trip.OwnerID = target.UserID
		_, err := repo.UpdateTrip(ctx, trip)
		return err
	})
}

// ----------------------------------------------------------------- invites --

// InviteInput creates a link. Zero MaxUses means unlimited, which is the right
// default for a link dropped in a group chat.
type InviteInput struct {
	Role      core.Role  `json:"role"`
	Label     string     `json:"label"`
	MaxUses   int        `json:"max_uses"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// CreateInvite mints a random token. The token, not the trip id, is what
// travels in the deep link, so trip ids never leak into chat history.
func (s *Service) CreateInvite(ctx context.Context, access Access, in InviteInput) (Invite, error) {
	if err := access.RequireManage(); err != nil {
		return Invite{}, err
	}
	role := in.Role
	if role == "" {
		role = core.RoleMember
	}
	v := core.NewValidator()
	v.Check(role == core.RoleMember || role == core.RoleAdmin, "role", "must be member or admin")
	v.Check(in.MaxUses >= 0 && in.MaxUses <= 100, "max_uses", "must be between 0 and 100")
	v.Length(in.Label, "label", 0, 60)
	if in.ExpiresAt != nil {
		v.Check(in.ExpiresAt.After(time.Now()), "expires_at", "must be in the future")
	}
	if err := v.Err(); err != nil {
		return Invite{}, err
	}

	invite, err := s.repo.InsertInvite(ctx, Invite{
		ID:        core.NewID(),
		TripID:    access.Trip.ID,
		Token:     newToken(),
		CreatedBy: access.User.ID,
		Role:      role,
		Label:     strings.TrimSpace(in.Label),
		MaxUses:   in.MaxUses,
		ExpiresAt: in.ExpiresAt,
	})
	if err != nil {
		return Invite{}, err
	}
	invite.URL = s.deepLink(InvitePrefix + invite.Token)
	return invite, nil
}

// Invites lists the live links for a trip.
func (s *Service) Invites(ctx context.Context, access Access) ([]Invite, error) {
	if err := access.Require(core.RoleAdmin); err != nil {
		return nil, err
	}
	list, err := s.repo.ListInvites(ctx, access.Trip.ID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].URL = s.deepLink(InvitePrefix + list[i].Token)
	}
	return list, nil
}

// RevokeInvite kills a link.
func (s *Service) RevokeInvite(ctx context.Context, access Access, inviteID core.ID) error {
	if err := access.RequireManage(); err != nil {
		return err
	}
	return s.repo.RevokeInvite(ctx, access.Trip.ID, inviteID)
}

// InvitePreview is what someone opening a link sees before committing.
type InvitePreview struct {
	Trip          Trip      `json:"trip"`
	OwnerName     string    `json:"owner_name"`
	MemberCount   int       `json:"member_count"`
	AlreadyJoined bool      `json:"already_joined"`
	Role          core.Role `json:"role"`
}

// PreviewInvite resolves a token for display without joining.
func (s *Service) PreviewInvite(ctx context.Context, token string, userID core.ID) (InvitePreview, error) {
	invite, trip, err := s.resolveInvite(ctx, token)
	if err != nil {
		return InvitePreview{}, err
	}
	counts, err := s.repo.MemberCounts(ctx, trip.ID)
	if err != nil {
		return InvitePreview{}, err
	}
	preview := InvitePreview{Trip: trip, MemberCount: counts.Active, Role: invite.Role}
	if owner, err := s.users.Get(ctx, trip.OwnerID); err == nil {
		preview.OwnerName = owner.DisplayName()
	}
	if !userID.IsZero() {
		if member, err := s.repo.MemberByUser(ctx, trip.ID, userID); err == nil {
			preview.AlreadyJoined = member.Status == core.MemberActive
		}
	}
	return preview, nil
}

// Join redeems an invite token. Re-joining with an existing membership is
// idempotent: it reactivates the row instead of failing, which is what a user
// tapping an old link a second time expects.
func (s *Service) Join(ctx context.Context, user users.User, token string) (Trip, Member, error) {
	invite, trip, err := s.resolveInvite(ctx, token)
	if err != nil {
		return Trip{}, Member{}, err
	}

	var member Member
	rejoined := false
	err = s.database.InTx(ctx, func(tx *gorm.DB) error {
		repo := NewRepo(tx)
		existing, err := repo.MemberByUser(ctx, trip.ID, user.ID)
		switch {
		case err == nil:
			if existing.Status == core.MemberActive {
				member = existing
				rejoined = true
				return nil
			}
			now := time.Now().UTC()
			existing.Status = core.MemberActive
			if existing.JoinedAt == nil {
				existing.JoinedAt = &now
			}
			member, err = repo.UpdateMember(ctx, existing)
			return err
		case core.CodeOf(err) == core.CodeNotFound:
			if err := repo.ConsumeInvite(ctx, invite.ID, time.Now().UTC()); err != nil {
				return err
			}
			now := time.Now().UTC()
			member, err = repo.InsertMember(ctx, Member{
				ID:          core.NewID(),
				TripID:      trip.ID,
				UserID:      user.ID,
				DisplayName: user.DisplayName(),
				Role:        invite.Role,
				Status:      core.MemberActive,
				JoinedAt:    &now,
			})
			return err
		default:
			return err
		}
	})
	if err != nil {
		return Trip{}, Member{}, err
	}
	if rejoined {
		return trip, member, nil
	}

	s.activity.Log(ctx, activity.Entry{
		TripID: trip.ID, ActorUserID: user.ID, ActorMemberID: member.ID,
		ActorName: member.DisplayName, Kind: activity.KindMemberJoined,
		Message: fmt.Sprintf("%s joined the trip", member.DisplayName),
	})
	s.notifyGroup(ctx, trip, user.ID, notify.Notification{
		TripID:   trip.ID,
		Category: notify.CategoryTripUpdates,
		Title:    "👥 " + trip.Title,
		Body:     fmt.Sprintf("%s joined the trip.", member.DisplayName),
	})
	return trip, member, nil
}

// resolveInvite validates a token and loads its trip.
func (s *Service) resolveInvite(ctx context.Context, token string) (Invite, Trip, error) {
	token = strings.TrimSpace(strings.TrimPrefix(token, InvitePrefix))
	if token == "" {
		return Invite{}, Trip{}, core.Invalid("invite token is required")
	}
	invite, err := s.repo.InviteByToken(ctx, token)
	if err != nil {
		return Invite{}, Trip{}, core.NotFound("invite")
	}
	if !invite.Usable(time.Now().UTC()) {
		return Invite{}, Trip{}, core.Conflict("this invite link is no longer valid")
	}
	trip, err := s.repo.TripByID(ctx, invite.TripID)
	if err != nil {
		return Invite{}, Trip{}, err
	}
	if !trip.Status.Writable() {
		return Invite{}, Trip{}, core.Conflict("this trip is archived")
	}
	return invite, trip, nil
}

// notifyGroup fans a message out to every active member except the actor.
func (s *Service) notifyGroup(ctx context.Context, trip Trip, exceptUserID core.ID, template notify.Notification) {
	userIDs, err := s.repo.ActiveMemberUserIDs(ctx, trip.ID)
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
		items = append(items, n)
	}
	_ = s.notify.Enqueue(ctx, items...)
}

// newToken returns a URL safe random string usable as a Telegram start payload
// (Telegram allows A-Z, a-z, 0-9, _ and - up to 64 characters).
func newToken() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("trips: entropy source failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf[:])
}
