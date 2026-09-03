// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A fuzz target no gate invokes is an unfuzzed parser: it compiles, its seed
// corpus runs as an ordinary test, and the fuzzing engine never explores past
// it. `make fuzz` is the one gate, so every FuzzXxx in the tree must be named
// there — the check is against the declared targets, not against a count.
func TestFuzzGateInvokesEveryFuzzTarget(t *testing.T) {
	root := repoRoot(t)

	declaration := regexp.MustCompile(`(?m)^func (Fuzz[A-Za-z0-9_]*)\(`)
	declared := map[string]string{}
	for _, path := range trackedFiles(t, root) {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		require.NoError(t, err)
		for _, match := range declaration.FindAllStringSubmatch(string(body), -1) {
			declared[match[1]] = path
		}
	}
	require.NotEmpty(t, declared, "no fuzz targets found; the walk is broken, not the gate")

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err)
	gate := fuzzTarget(t, string(makefile))

	missing := make([]string, 0)
	for name, path := range declared {
		if !strings.Contains(gate, "-fuzz="+name) {
			missing = append(missing, name+" ("+path+")")
		}
	}
	sort.Strings(missing)
	assert.Empty(t, missing,
		"these fuzz targets are declared but no gate invokes them: %v", missing)
}

// fuzzTarget returns the recipe lines of the Makefile's `fuzz` target.
func fuzzTarget(t *testing.T, makefile string) string {
	t.Helper()
	var recipe []string
	inTarget := false
	for _, line := range strings.Split(makefile, "\n") {
		switch {
		case strings.HasPrefix(line, "fuzz:"):
			inTarget = true
		case inTarget && strings.HasPrefix(line, "\t"):
			recipe = append(recipe, line)
		case inTarget:
			inTarget = false
		}
	}
	require.NotEmpty(t, recipe, "the Makefile declares no fuzz recipe")
	return strings.Join(recipe, "\n")
}
