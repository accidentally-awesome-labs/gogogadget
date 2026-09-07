package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// SaaS compositions stay presentation-only: display strings and slots, never a
// billing or identity type, so a module can reuse them without pulling a
// provider package.
func TestMemberItemTakesDisplayValuesOnly(t *testing.T) {
	typ := typeOf(MemberItemOpts{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		pkg := field.Type.PkgPath()
		assert.NotContains(t, pkg, "internal/billing",
			"%s.%s pulls a provider package into the leaf ui layer", typ.Name(), field.Name)
		assert.NotContains(t, pkg, "internal/identity",
			"%s.%s pulls a provider package into the leaf ui layer", typ.Name(), field.Name)
		assert.NotContains(t, pkg, "internal/db/sqlc",
			"%s.%s pulls a database type into the leaf ui layer", typ.Name(), field.Name)
	}
}

// An unaccepted invitation is a different state from membership and needs a
// different action, so it is marked rather than rendered as a member.
func TestMemberItemMarksPendingInvitations(t *testing.T) {
	pending := renderComponent(t, MemberItem(MemberItemOpts{
		Name: "Grace", Email: "grace@example.com", RoleLabel: "Member", Pending: true,
	}))
	assert.Contains(t, pending, "pending")
	assert.Contains(t, pending, "badge-warn")

	member := renderComponent(t, MemberItem(MemberItemOpts{
		Name: "Ada", Email: "ada@example.com", RoleLabel: "Owner",
	}))
	assert.NotContains(t, member, "pending")
}
