package trips

import (
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

func accessFor(role core.Role, status core.MemberStatus, tripStatus core.TripStatus) Access {
	return Access{
		Trip:   Trip{ID: core.NewID(), Status: tripStatus},
		Member: Member{ID: core.NewID(), Role: role, Status: status},
	}
}

// The permission matrix from the product spec, expressed as a test.
func TestRequireRole(t *testing.T) {
	cases := []struct {
		role core.Role
		min  core.Role
		ok   bool
	}{
		{core.RoleOwner, core.RoleAdmin, true},
		{core.RoleAdmin, core.RoleAdmin, true},
		{core.RoleMember, core.RoleAdmin, false},
		{core.RoleMember, core.RoleMember, true},
		{core.RoleAdmin, core.RoleOwner, false},
	}
	for _, tc := range cases {
		access := accessFor(tc.role, core.MemberActive, core.TripPlanning)
		err := access.Require(tc.min)
		if tc.ok && err != nil {
			t.Errorf("%s should satisfy %s: %v", tc.role, tc.min, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("%s must not satisfy %s", tc.role, tc.min)
			} else if core.CodeOf(err) != core.CodeForbidden {
				t.Errorf("expected forbidden, got %s", core.CodeOf(err))
			}
		}
	}
}

// An archived trip is frozen for everybody, owner included.
func TestRequireWriteRejectsArchivedTrips(t *testing.T) {
	access := accessFor(core.RoleOwner, core.MemberActive, core.TripArchived)
	err := access.RequireWrite()
	if err == nil {
		t.Fatal("an archived trip must reject writes")
	}
	if core.CodeOf(err) != core.CodeConflict {
		t.Errorf("code = %s, want conflict", core.CodeOf(err))
	}
}

// Anyone whose membership is not active can look, not touch. Access already
// turns declined and removed into a 404, so this is the second line of
// defence rather than the first.
func TestRequireWriteRejectsInactiveMembers(t *testing.T) {
	for _, status := range []core.MemberStatus{core.MemberDeclined, core.MemberRemoved} {
		access := accessFor(core.RoleMember, status, core.TripPlanning)
		err := access.RequireWrite()
		if err == nil {
			t.Fatalf("a %s member must not write", status)
		}
		if core.CodeOf(err) != core.CodeForbidden {
			t.Errorf("%s: code = %s, want forbidden", status, core.CodeOf(err))
		}
	}
}

func TestRequireManage(t *testing.T) {
	if err := accessFor(core.RoleAdmin, core.MemberActive, core.TripPlanning).RequireManage(); err != nil {
		t.Errorf("an active admin can manage: %v", err)
	}
	if err := accessFor(core.RoleMember, core.MemberActive, core.TripPlanning).RequireManage(); err == nil {
		t.Error("a plain member must not manage")
	}
	if err := accessFor(core.RoleOwner, core.MemberActive, core.TripArchived).RequireManage(); err == nil {
		t.Error("archival beats role")
	}
}

// A member may always act on their own row; acting on somebody else's needs
// the organiser role.
func TestRequireSelfOrManage(t *testing.T) {
	member := accessFor(core.RoleMember, core.MemberActive, core.TripPlanning)
	if err := member.RequireSelfOrManage(member.Member.ID); err != nil {
		t.Errorf("a member may act on themselves: %v", err)
	}
	if err := member.RequireSelfOrManage(core.NewID()); err == nil {
		t.Error("a member must not act on somebody else")
	}

	admin := accessFor(core.RoleAdmin, core.MemberActive, core.TripPlanning)
	if err := admin.RequireSelfOrManage(core.NewID()); err != nil {
		t.Errorf("an organiser may act on anyone: %v", err)
	}
}

func TestAccessHelpers(t *testing.T) {
	owner := accessFor(core.RoleOwner, core.MemberActive, core.TripPlanning)
	if !owner.IsOwner() || !owner.IsManager() || owner.Role() != core.RoleOwner {
		t.Error("owner helpers are wrong")
	}
	admin := accessFor(core.RoleAdmin, core.MemberActive, core.TripPlanning)
	if admin.IsOwner() || !admin.IsManager() {
		t.Error("admin helpers are wrong")
	}
	member := accessFor(core.RoleMember, core.MemberActive, core.TripPlanning)
	if member.IsOwner() || member.IsManager() {
		t.Error("member helpers are wrong")
	}
}

func TestMemberInitials(t *testing.T) {
	cases := map[string]string{
		"Andrei Varabyeu": "AV",
		"Vasya":           "VA",
		"X":               "X",
		"":                "??",
	}
	for name, want := range cases {
		if got := (Member{DisplayName: name}).Initials(); got != want {
			t.Errorf("Initials(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestInviteUsable(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	if !(Invite{}).Usable(now) {
		t.Error("a fresh unlimited invite is usable")
	}
	if (Invite{RevokedAt: &past}).Usable(now) {
		t.Error("a revoked invite is not usable")
	}
	if (Invite{ExpiresAt: &past}).Usable(now) {
		t.Error("an expired invite is not usable")
	}
	if !(Invite{ExpiresAt: &future}).Usable(now) {
		t.Error("an invite expiring later is usable")
	}
	if (Invite{MaxUses: 1, Uses: 1}).Usable(now) {
		t.Error("a used-up single-use invite is not usable")
	}
	if !(Invite{MaxUses: 2, Uses: 1}).Usable(now) {
		t.Error("an invite with uses left is usable")
	}
	// MaxUses zero means unlimited, which is the default for a link dropped
	// into a group chat.
	if !(Invite{MaxUses: 0, Uses: 50}).Usable(now) {
		t.Error("max_uses 0 means unlimited")
	}
}

func TestTripNights(t *testing.T) {
	trip := Trip{
		StartDate: core.NewDate(2026, time.September, 23),
		EndDate:   core.NewDate(2026, time.September, 24),
	}
	if got := trip.Nights(); got != 1 {
		t.Errorf("Nights = %d, want 1", got)
	}
	dayTrip := Trip{
		StartDate: core.NewDate(2026, time.September, 23),
		EndDate:   core.NewDate(2026, time.September, 23),
	}
	if got := dayTrip.Nights(); got != 0 {
		t.Errorf("a day trip has %d nights, want 0", got)
	}
}
