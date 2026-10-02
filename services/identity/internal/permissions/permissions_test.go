package permissions

import "testing"

func TestRole(t *testing.T) {
	if !RoleAdmin.AtLeast(RoleRead) || !RoleWrite.AtLeast(RoleWrite) ||
		RoleRead.AtLeast(RoleWrite) || RoleNone.AtLeast(RoleRead) ||
		RoleAdmin.AtLeast(RoleNone) {
		t.Error("ordine dei ruoli errato")
	}
}

func TestMax(t *testing.T) {
	if Max(RoleRead, RoleWrite) != RoleWrite || Max(RoleAdmin, RoleNone) != RoleAdmin || Max(RoleNone, RoleNone) != RoleNone {
		t.Error("Max errato")
	}
}
