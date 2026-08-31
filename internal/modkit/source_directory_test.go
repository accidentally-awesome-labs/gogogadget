package modkit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A developer's tree and a fresh checkout must resolve the SAME registry
// commit from the same source. While `.env` was hashed they did not: every
// developer has one and CI has none, so the committed generated headers
// depended on whose machine ran the generator, and CI's "generated code is
// committed and fresh" check could not pass from any real working tree.
//
// The property is stated as "local artifacts do not participate in identity"
// rather than as a list, so a new artifact name is a failing test rather than a
// silent divergence discovered in CI.
func TestLocalArtifactsDoNotChangeTheRegistryCommit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[]}`))
	writeTestFile(t, root, "registry/modules/element/x/module.json", []byte(`{"id":"ggg/element/x"}`))
	writeTestFile(t, root, "internal/web/real.go", []byte("package web\n"))

	baseline, err := DirectorySource{Root: root}.Resolve(context.Background(), "", "")
	require.NoError(t, err)
	require.NotEmpty(t, baseline.Commit)

	// Everything a working tree accumulates and a clone does not.
	artifacts := map[string]string{
		".env":                         "CLERK_SECRET_KEY=sk_test_local\n",
		".env.local":                   "OVERRIDE=1\n",
		".DS_Store":                    "\x00\x01",
		"coverage.out":                 "mode: set\n",
		"tmp/scratch":                  "build output\n",
		"internal/web/tmp/scratch":     "a tmp dir one level down\n",
		"internal/web/node_modules/x":  "a nested dependency tree\n",
		"bin/tailwindcss":              "binary\n",
		"e2e/test-results/report.json": "{}\n",
	}
	for rel, body := range artifacts {
		writeTestFile(t, root, rel, []byte(body))
	}

	withArtifacts, err := DirectorySource{Root: root}.Resolve(context.Background(), "", "")
	require.NoError(t, err)
	assert.Equal(t, baseline.Commit, withArtifacts.Commit,
		"a local artifact changed the registry commit, so a working tree and a fresh checkout will disagree about generated output")
}

// The converse: a real source file MUST change it, or the commit is not an
// identity at all and the exclusion has been drawn too wide.
func TestRealSourceStillChangesTheRegistryCommit(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[]}`))
	writeTestFile(t, root, "internal/web/real.go", []byte("package web\n"))

	before, err := DirectorySource{Root: root}.Resolve(context.Background(), "", "")
	require.NoError(t, err)

	writeTestFile(t, root, "internal/web/real.go", []byte("package web\n\nvar Added = 1\n"))
	after, err := DirectorySource{Root: root}.Resolve(context.Background(), "", "")
	require.NoError(t, err)

	assert.NotEqual(t, before.Commit, after.Commit,
		"editing distributed source must move the registry commit")
}
