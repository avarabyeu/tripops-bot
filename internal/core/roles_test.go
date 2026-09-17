package core

import "testing"

// The permission model is three roles and nothing else, so the table below is
// the complete specification of who may do what at the trip level.
func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		role Role
		min  Role
		want bool
	}{
		{RoleOwner, RoleOwner, true},
		{RoleOwner, RoleAdmin, true},
		{RoleOwner, RoleMember, true},
		{RoleAdmin, RoleOwner, false},
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleMember, true},
		{RoleMember, RoleOwner, false},
		{RoleMember, RoleAdmin, false},
		{RoleMember, RoleMember, true},
		{Role("ghost"), RoleMember, false},
	}
	for _, tc := range cases {
		if got := tc.role.AtLeast(tc.min); got != tc.want {
			t.Errorf("%s.AtLeast(%s) = %v, want %v", tc.role, tc.min, got, tc.want)
		}
	}
}

func TestRoleValid(t *testing.T) {
	for _, r := range []Role{RoleOwner, RoleAdmin, RoleMember} {
		if !r.Valid() {
			t.Errorf("%s should be valid", r)
		}
	}
	if Role("superuser").Valid() {
		t.Error("unknown roles must not validate")
	}
}

func TestTripStatusWritable(t *testing.T) {
	for _, s := range []TripStatus{TripPlanning, TripActive, TripCompleted} {
		if !s.Writable() {
			t.Errorf("%s should be writable", s)
		}
	}
	if TripArchived.Writable() {
		t.Error("an archived trip must not be writable")
	}
}

func TestMemberStatusValid(t *testing.T) {
	if !MemberActive.Valid() || !MemberInvited.Valid() || !MemberDeclined.Valid() || !MemberRemoved.Valid() {
		t.Error("all known member statuses must validate")
	}
	if MemberStatus("lurking").Valid() {
		t.Error("unknown statuses must not validate")
	}
}
