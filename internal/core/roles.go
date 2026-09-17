package core

// Role is a participant's role inside one trip. The MVP deliberately has no
// granular ACLs: every permission question reduces to a role comparison.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

var roleRank = map[Role]int{RoleMember: 1, RoleAdmin: 2, RoleOwner: 3}

// Valid reports whether the role is one of the three known roles.
func (r Role) Valid() bool { _, ok := roleRank[r]; return ok }

// AtLeast reports whether r has at least the authority of min.
func (r Role) AtLeast(min Role) bool { return roleRank[r] >= roleRank[min] }

// MemberStatus is the lifecycle of a participant's membership.
type MemberStatus string

const (
	MemberInvited  MemberStatus = "invited"
	MemberActive   MemberStatus = "active"
	MemberDeclined MemberStatus = "declined"
	MemberRemoved  MemberStatus = "removed"
)

func (s MemberStatus) Valid() bool {
	switch s {
	case MemberInvited, MemberActive, MemberDeclined, MemberRemoved:
		return true
	}
	return false
}

// TripStatus is the lifecycle of a trip.
type TripStatus string

const (
	TripPlanning  TripStatus = "planning"
	TripActive    TripStatus = "active"
	TripCompleted TripStatus = "completed"
	TripArchived  TripStatus = "archived"
)

func (s TripStatus) Valid() bool {
	switch s {
	case TripPlanning, TripActive, TripCompleted, TripArchived:
		return true
	}
	return false
}

// Writable reports whether trip content may still be changed.
func (s TripStatus) Writable() bool { return s != TripArchived }
