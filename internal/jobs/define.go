// The module-facing job declaration. A module declares a kind, whether it may
// back a schedule, its attempt budget, and a typed handler; the generated
// dispatcher and schedulable catalog are built from those declarations.
//
// Queue mechanics — claiming with SKIP LOCKED, the visibility timeout,
// exponential backoff, and dead-lettering — stay in the core and are not
// something a module can influence.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
)

// DefaultMaxAttempts is the budget a declaration gets when it omits one. Zero
// means "unspecified", not "never retry": a zero in a struct literal is almost
// always an omission, and a job that never retries is a silent data-loss bug the
// first time the network blips.
const DefaultMaxAttempts = 8

// Attempt is the retry state of the claimed row. Most handlers do not care, but
// a few must know they are on their last attempt so they can record a terminal
// outcome of their own — the webhook deliverer marks its delivery permanently
// dead exactly when the job is about to be dead-lettered.
type Attempt struct {
	// Number is this attempt, 1-based (ClaimJob has already incremented it).
	Number int
	// Max is the row's budget. It comes from the row, not the declaration, so a
	// job keeps the budget it was enqueued under.
	Max int
}

// Last reports whether a failure now exhausts the row's budget.
func (a Attempt) Last() bool { return a.Number >= a.Max }

// Definition is one declared job kind. Handle takes the raw payload because the
// dispatcher is generated from data and cannot know each module's payload type;
// Define recovers the typing at the module boundary.
type Definition struct {
	Kind        string
	Schedulable bool
	MaxAttempts int
	Handle      func(context.Context, json.RawMessage, Attempt) error
}

// Define declares a job kind with a typed payload. The handler receives its own
// payload type, so a module never writes an unmarshal or a type assertion, and a
// malformed payload surfaces as an error the retry machinery can see rather than
// a panic inside the worker loop.
func Define[P any](kind string, schedulable bool, maxAttempts int, h func(context.Context, P) error) Definition {
	return DefineWithAttempt(kind, schedulable, maxAttempts,
		func(ctx context.Context, payload P, _ Attempt) error { return h(ctx, payload) })
}

// DefineWithAttempt is Define for the rare handler that must know its retry
// state. It is a separate constructor so the common case stays free of a
// parameter almost nobody needs, and so a handler that does need it says so.
func DefineWithAttempt[P any](kind string, schedulable bool, maxAttempts int,
	h func(context.Context, P, Attempt) error) Definition {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	return Definition{
		Kind:        kind,
		Schedulable: schedulable,
		MaxAttempts: maxAttempts,
		Handle: func(ctx context.Context, raw json.RawMessage, attempt Attempt) error {
			var payload P
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &payload); err != nil {
					return fmt.Errorf("job %s payload: %w", kind, err)
				}
			}
			return h(ctx, payload, attempt)
		},
	}
}
