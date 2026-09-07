package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// SaaS compositions stay presentation-only: display strings and slots, never a
// billing or identity type, so a module can reuse them without pulling a
// provider package.
func TestSettingsSectionTakesDisplayValuesOnly(t *testing.T) {
	typ := typeOf(SettingsSectionOpts{})
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
