package gggcli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// planFor projects a modkit plan into the preview boundary. The offline hint
// rides along unexported so Apply re-resolves the same engine the preview
// used; the exported Plan shape stays exactly {RunID, Command, Local, Remote,
// Diagnostics}.
func (c *Controller) planFor(command string, local *modkit.Plan, offline bool) Plan {
	plan := Plan{
		Command:     command,
		Local:       local,
		Diagnostics: local.Diagnostics,
		offline:     offline,
	}
	if local != nil {
		env := planEnvelope(*local, command, exitOK)
		plan.RunID = env.RunID
	}
	return plan
}

// planEnvelope projects a modkit.Plan onto the envelope. Generated paths are
// read off the plan's own classification so the report cannot drift from the
// transaction. The command name is set before the run id is derived, so the
// same plan reports the same id the old envelope path produced.
func planEnvelope(plan modkit.Plan, command string, exit int) modkit.Envelope {
	generated := make([]string, 0)
	for _, change := range plan.Changes {
		if change.Class == modkit.DestinationGenerated {
			generated = append(generated, change.Path)
		}
	}
	sort.Strings(generated)
	return normalizeEnvelope(modkit.Envelope{
		Command:        command,
		OK:             exit == exitOK,
		RegistryCommit: plan.RegistryCommit,
		Resolved:       plan.Resolved,
		Changes:        plan.Changes,
		Generated:      generated,
		Conflicts:      plan.Conflicts,
		Diagnostics:    plan.Diagnostics,
		Exit:           exit,
	})
}

// normalizeEnvelope fills the fixed envelope's empty collections and derives
// the run id, so a missing key can never reach a machine consumer.
func normalizeEnvelope(env modkit.Envelope) modkit.Envelope {
	if env.Resolved == nil {
		env.Resolved = []string{}
	}
	if env.Changes == nil {
		env.Changes = []modkit.Change{}
	}
	if env.Generated == nil {
		env.Generated = []string{}
	}
	if env.Conflicts == nil {
		env.Conflicts = []modkit.Conflict{}
	}
	if env.Diagnostics == nil {
		env.Diagnostics = []modkit.Diagnostic{}
	}
	if env.RunID == "" {
		env.RunID = envelopeRunID(env)
	}
	return env
}

// envelopeRunID derives a stable id from the envelope's content, so the same
// plan reports the same run id and a changed plan cannot reuse one.
func envelopeRunID(env modkit.Envelope) string {
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\n%s\n%d\n", env.Command, env.RegistryCommit, env.Exit)
	for _, id := range env.Resolved {
		fmt.Fprintf(sum, "resolved:%s\n", id)
	}
	for _, change := range env.Changes {
		fmt.Fprintf(sum, "change:%s:%s:%s\n", change.Path, change.Kind, change.Class)
	}
	for _, conflict := range env.Conflicts {
		fmt.Fprintf(sum, "conflict:%s:%s\n", conflict.Module, conflict.Path)
	}
	return hex.EncodeToString(sum.Sum(nil))[:16]
}

// countDrift reports how many entries in the plan would change on disk.
func countDrift(plan modkit.Plan) int {
	n := 0
	for _, change := range plan.Changes {
		if change.Kind != modkit.ChangeUnchanged {
			n++
		}
	}
	return n
}

// offlineHint recovers the source mode the preview resolved with, and
// whether the plan carries a local modkit plan at all.
func (p Plan) offlineHint() (bool, bool) { return p.offline, p.Local != nil }

// plannerFailure marks a planning failure: the command's declared behavior is
// to emit the command_failed envelope for it, while pre-plan refusals carry
// only the coded error.
type plannerFailure struct{ err error }

func (p plannerFailure) Error() string { return p.err.Error() }
func (p plannerFailure) Unwrap() error { return p.err }

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, v := range dst {
		seen[v] = struct{}{}
	}
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		dst = append(dst, v)
	}
	sort.Strings(dst)
	return dst
}

func dedupeSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
