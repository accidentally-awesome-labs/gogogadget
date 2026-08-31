package storage_test

import (
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/stretchr/testify/assert"
	"path/filepath"
	"testing"
)

func TestNewKeyShape(t *testing.T) {
	k := storage.NewKey("org_42", "Quarterly Report FINAL v2.pdf")
	dir, base := filepath.Split(k)
	assert.Equal(t, "orgs/org_42/", dir)
	assert.Regexp(t, `^[0-9a-f]{32}\.pdf$`, base)
	k2 := storage.NewKey("org_42", "no-extension")
	_, base2 := filepath.Split(k2)
	assert.Regexp(t, `^[0-9a-f]{32}$`, base2)
	k3 := storage.NewKey("org_42", ".hidden-longextensionfile")
	_, base3 := filepath.Split(k3)
	assert.NotContains(t, base3, "hidden")
}
