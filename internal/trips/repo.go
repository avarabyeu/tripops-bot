package trips

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/db"
)

// Repo is the data access for trips, members and invites. Constructed with
// either the pool handle or a transaction so multi-table operations stay
// atomic.
type Repo struct{ db *gorm.DB }

func NewRepo(gdb *gorm.DB) *Repo { return &Repo{db: gdb} }

// ------------------------------------------------------------------- trips --

func (r *Repo) InsertTrip(ctx context.Context, t Trip) (Trip, error) {
	now := time.Now().UTC()
	t.CreatedAt, t.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&t).Error; err != nil {
		return Trip{}, core.Internal(fmt.Errorf("trips: insert: %w", err))
	}
	return t, nil
}

func (r *Repo) TripByID(ctx context.Context, id core.ID) (Trip, error) {
	var t Trip
	err := r.db.WithContext(ctx).First(&t, "id = ?", id).Error
	if db.IsNotFound(err) {
		return Trip{}, core.NotFound("trip")
	}
	if err != nil {
		return Trip{}, core.Internal(fmt.Errorf("trips: by id: %w", err))
	}
	return t, nil
}

// UpdateTrip writes the mutable fields of an already validated trip.
func (r *Repo) UpdateTrip(ctx context.Context, t Trip) (Trip, error) {
	t.UpdatedAt = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&Trip{}).Where("id = ?", t.ID).
		Select("title", "description", "start_date", "end_date", "timezone",
			"currency", "status", "owner_id", "updated_at").
		Updates(&t)
	if res.Error != nil {
		return Trip{}, core.Internal(fmt.Errorf("trips: update: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Trip{}, core.NotFound("trip")
	}
	return r.TripByID(ctx, t.ID)
}

// TripSummary is a trip plus the caller's relationship to it, which is what
// the trip list needs and nothing more.
type TripSummary struct {
	Trip
	Role          core.Role `json:"role"`
	MemberCount   int       `json:"member_count"`
	PendingCount  int       `json:"pending_count"`
	OpenDecisions int       `json:"open_decisions"`
}

// ListTripsForUser returns every trip the user belongs to, upcoming first and
// archived last.
//
// The counters are correlated subqueries rather than joins with GROUP BY: they
// read the same on both engines and keep the row shape flat.
func (r *Repo) ListTripsForUser(ctx context.Context, userID core.ID, includeArchived bool) ([]TripSummary, error) {
	query := r.db.WithContext(ctx).
		Table("trips AS t").
		Select(`t.*, m.role AS role,
			(SELECT count(*) FROM trip_members x WHERE x.trip_id = t.id AND x.status = 'active') AS member_count,
			(SELECT count(*) FROM trip_members x WHERE x.trip_id = t.id AND x.status = 'invited') AS pending_count,
			(SELECT count(*) FROM decisions d WHERE d.trip_id = t.id AND d.status = 'open') AS open_decisions`).
		Joins("JOIN trip_members m ON m.trip_id = t.id AND m.user_id = ?", userID).
		Where("m.status IN ?", []string{string(core.MemberActive), string(core.MemberInvited)}).
		Order("CASE WHEN t.status = 'archived' THEN 1 ELSE 0 END, t.start_date DESC, t.created_at DESC")
	if !includeArchived {
		query = query.Where("t.status <> ?", string(core.TripArchived))
	}

	out := []TripSummary{}
	if err := query.Scan(&out).Error; err != nil {
		return nil, core.Internal(fmt.Errorf("trips: list for user: %w", err))
	}
	return out, nil
}

// UpcomingTrips lists non-archived trips whose end date has not passed. The
// scheduler walks them to decide what to remind people about.
func (r *Repo) UpcomingTrips(ctx context.Context, today core.Date, horizonDays int) ([]Trip, error) {
	out := []Trip{}
	err := r.db.WithContext(ctx).
		Where("status IN ?", []string{string(core.TripPlanning), string(core.TripActive)}).
		Where("end_date >= ?", today).
		Where("start_date <= ?", today.AddDays(horizonDays)).
		Order("start_date").
		Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("trips: upcoming: %w", err))
	}
	return out, nil
}

// ----------------------------------------------------------------- members --

// memberRow is a member joined with the Telegram profile the UI renders.
type memberRow struct {
	Member
	JoinedTelegramID int64
	JoinedUsername   string
	JoinedPhotoURL   string
}

const memberSelect = `m.*, u.telegram_id AS joined_telegram_id,
	u.username AS joined_username, u.photo_url AS joined_photo_url`

func (row memberRow) member() Member {
	m := row.Member
	m.TelegramID = row.JoinedTelegramID
	m.Username = row.JoinedUsername
	m.PhotoURL = row.JoinedPhotoURL
	return m
}

func (r *Repo) memberQuery(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).
		Table("trip_members AS m").
		Select(memberSelect).
		Joins("JOIN users u ON u.id = m.user_id")
}

func (r *Repo) InsertMember(ctx context.Context, m Member) (Member, error) {
	now := time.Now().UTC()
	m.CreatedAt, m.UpdatedAt = now, now
	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		if db.IsDuplicate(err) {
			// Either the person is already on the trip or the trip already has
			// an owner; both are conflicts the caller can act on.
			return Member{}, core.Conflict("this person is already on the trip")
		}
		return Member{}, core.Internal(fmt.Errorf("trips: insert member: %w", err))
	}
	return r.MemberByID(ctx, m.ID)
}

func (r *Repo) MemberByID(ctx context.Context, id core.ID) (Member, error) {
	var row memberRow
	err := r.memberQuery(ctx).Where("m.id = ?", id).Limit(1).Scan(&row).Error
	if err != nil {
		return Member{}, core.Internal(fmt.Errorf("trips: member by id: %w", err))
	}
	if row.ID.IsZero() {
		return Member{}, core.NotFound("member")
	}
	return row.member(), nil
}

func (r *Repo) MemberByUser(ctx context.Context, tripID, userID core.ID) (Member, error) {
	var row memberRow
	err := r.memberQuery(ctx).Where("m.trip_id = ? AND m.user_id = ?", tripID, userID).
		Limit(1).Scan(&row).Error
	if err != nil {
		return Member{}, core.Internal(fmt.Errorf("trips: member by user: %w", err))
	}
	if row.ID.IsZero() {
		return Member{}, core.NotFound("membership")
	}
	return row.member(), nil
}

// ListMembers returns the group ordered owner first, then admins, then members.
func (r *Repo) ListMembers(ctx context.Context, tripID core.ID, includeRemoved bool) ([]Member, error) {
	query := r.memberQuery(ctx).Where("m.trip_id = ?", tripID).
		Order("CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END, m.created_at")
	if !includeRemoved {
		query = query.Where("m.status <> ?", string(core.MemberRemoved))
	}
	var scanned []memberRow
	if err := query.Scan(&scanned).Error; err != nil {
		return nil, core.Internal(fmt.Errorf("trips: list members: %w", err))
	}
	out := make([]Member, 0, len(scanned))
	for _, row := range scanned {
		out = append(out, row.member())
	}
	return out, nil
}

func (r *Repo) UpdateMember(ctx context.Context, m Member) (Member, error) {
	m.UpdatedAt = time.Now().UTC()
	res := r.db.WithContext(ctx).Model(&Member{}).Where("id = ?", m.ID).
		Select("display_name", "role", "status", "joined_at", "updated_at").
		Updates(map[string]any{
			"display_name": m.DisplayName,
			"role":         string(m.Role),
			"status":       string(m.Status),
			"joined_at":    m.JoinedAt,
			"updated_at":   m.UpdatedAt,
		})
	if res.Error != nil {
		if db.IsDuplicate(res.Error) {
			return Member{}, core.Conflict("the trip already has an owner")
		}
		return Member{}, core.Internal(fmt.Errorf("trips: update member: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return Member{}, core.NotFound("member")
	}
	return r.MemberByID(ctx, m.ID)
}

// MemberCounts is the group summary used by the dashboard.
//
// COUNT(CASE WHEN …) rather than PostgreSQL's FILTER clause: the aggregate has
// to run on SQLite too.
func (r *Repo) MemberCounts(ctx context.Context, tripID core.ID) (MemberCounts, error) {
	var c MemberCounts
	err := r.db.WithContext(ctx).Table("trip_members").
		Select(`count(CASE WHEN status IN ('active','invited') THEN 1 END) AS total,
			count(CASE WHEN status = 'active' THEN 1 END) AS active,
			count(CASE WHEN status = 'invited' THEN 1 END) AS invited`).
		Where("trip_id = ?", tripID).
		Scan(&c).Error
	if err != nil {
		return MemberCounts{}, core.Internal(fmt.Errorf("trips: member counts: %w", err))
	}
	return c, nil
}

// ActiveMemberUserIDs is what the notification fan-out needs.
func (r *Repo) ActiveMemberUserIDs(ctx context.Context, tripID core.ID) ([]core.ID, error) {
	out := []core.ID{}
	err := r.db.WithContext(ctx).Model(&Member{}).
		Where("trip_id = ? AND status = ?", tripID, string(core.MemberActive)).
		Pluck("user_id", &out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("trips: active user ids: %w", err))
	}
	return out, nil
}

// ----------------------------------------------------------------- invites --

func (r *Repo) InsertInvite(ctx context.Context, i Invite) (Invite, error) {
	i.CreatedAt = time.Now().UTC()
	if err := r.db.WithContext(ctx).Create(&i).Error; err != nil {
		return Invite{}, core.Internal(fmt.Errorf("trips: insert invite: %w", err))
	}
	return i, nil
}

func (r *Repo) InviteByToken(ctx context.Context, token string) (Invite, error) {
	var i Invite
	err := r.db.WithContext(ctx).First(&i, "token = ?", token).Error
	if db.IsNotFound(err) {
		return Invite{}, core.NotFound("invite")
	}
	if err != nil {
		return Invite{}, core.Internal(fmt.Errorf("trips: invite by token: %w", err))
	}
	return i, nil
}

func (r *Repo) ListInvites(ctx context.Context, tripID core.ID) ([]Invite, error) {
	out := []Invite{}
	err := r.db.WithContext(ctx).
		Where("trip_id = ? AND revoked_at IS NULL", tripID).
		Order("created_at DESC").
		Find(&out).Error
	if err != nil {
		return nil, core.Internal(fmt.Errorf("trips: list invites: %w", err))
	}
	return out, nil
}

// ConsumeInvite increments the use counter, re-checking in the same statement
// that the invite is still usable so two people redeeming a single-use link
// race safely.
func (r *Repo) ConsumeInvite(ctx context.Context, id core.ID, now time.Time) error {
	res := r.db.WithContext(ctx).Model(&Invite{}).
		Where("id = ?", id).
		Where("revoked_at IS NULL").
		Where("expires_at IS NULL OR expires_at > ?", now).
		Where("max_uses = 0 OR uses < max_uses").
		Update("uses", gorm.Expr("uses + 1"))
	if res.Error != nil {
		return core.Internal(fmt.Errorf("trips: consume invite: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.Conflict("this invite link is no longer valid")
	}
	return nil
}

func (r *Repo) RevokeInvite(ctx context.Context, tripID, id core.ID) error {
	res := r.db.WithContext(ctx).Model(&Invite{}).
		Where("id = ? AND trip_id = ? AND revoked_at IS NULL", id, tripID).
		Update("revoked_at", time.Now().UTC())
	if res.Error != nil {
		return core.Internal(fmt.Errorf("trips: revoke invite: %w", res.Error))
	}
	if res.RowsAffected == 0 {
		return core.NotFound("invite")
	}
	return nil
}
