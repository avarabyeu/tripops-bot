package trips

import (
	"context"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// Access is the answer to "may this user do this to this trip". Every module
// starts a request by obtaining one; nothing downstream re-derives permissions.
type Access struct {
	Trip   Trip
	Member Member
	User   users.User
}

// Authorizer is what other modules depend on, so they need nothing from this
// package beyond the permission check itself.
type Authorizer interface {
	Access(ctx context.Context, tripID, userID core.ID) (Access, error)
}

// Access loads the trip and the caller's membership, or a not-found error.
//
// A non-member gets "not found" rather than "forbidden": trip ids are not
// secret, but confirming that one exists to someone who was not invited leaks
// more than it helps.
func (s *Service) Access(ctx context.Context, tripID, userID core.ID) (Access, error) {
	trip, err := s.repo.TripByID(ctx, tripID)
	if err != nil {
		return Access{}, err
	}
	member, err := s.repo.MemberByUser(ctx, tripID, userID)
	if err != nil {
		if core.CodeOf(err) == core.CodeNotFound {
			return Access{}, core.NotFound("trip")
		}
		return Access{}, err
	}
	if member.Status == core.MemberRemoved || member.Status == core.MemberDeclined {
		return Access{}, core.NotFound("trip")
	}
	user, err := s.users.Get(ctx, userID)
	if err != nil {
		return Access{}, err
	}
	return Access{Trip: trip, Member: member, User: user}, nil
}

// Role is the caller's role on this trip.
func (a Access) Role() core.Role { return a.Member.Role }

// IsOwner reports whether the caller owns the trip.
func (a Access) IsOwner() bool { return a.Member.Role == core.RoleOwner }

// IsManager reports whether the caller is an owner or admin, which is the line
// most "organiser" actions sit on.
func (a Access) IsManager() bool { return a.Member.Role.AtLeast(core.RoleAdmin) }

// Require fails unless the caller holds at least the given role.
func (a Access) Require(min core.Role) error {
	if !a.Member.Role.AtLeast(min) {
		return core.Forbidden("this action requires the %s role", min)
	}
	return nil
}

// RequireWrite fails when the trip is archived or the caller has only been
// invited and has not joined yet. Use it before any mutation.
func (a Access) RequireWrite() error {
	if !a.Trip.Status.Writable() {
		return core.Conflict("this trip is archived and cannot be changed")
	}
	if a.Member.Status != core.MemberActive {
		return core.Forbidden("join the trip before changing anything")
	}
	return nil
}

// RequireManage combines the two checks every organiser action needs.
func (a Access) RequireManage() error {
	if err := a.RequireWrite(); err != nil {
		return err
	}
	return a.Require(core.RoleAdmin)
}

// RequireSelfOrManage allows a member to act on their own row and an organiser
// to act on anyone's.
func (a Access) RequireSelfOrManage(memberID core.ID) error {
	if err := a.RequireWrite(); err != nil {
		return err
	}
	if a.Member.ID == memberID || a.IsManager() {
		return nil
	}
	return core.Forbidden("you can only change your own entry")
}
