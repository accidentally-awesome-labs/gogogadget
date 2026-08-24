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

// Definition is one declared job kind. Handle takes the raw payload because the
// dispatcher is generated from data and cannot know each module's payload type;
// Define recovers the typing at the module boundary.
type Definition struct {
	Kind        string
	Schedulable bool
	MaxAttempts int
	Handle      func(context.Context, json.RawMessage) error
}

// Define declares a job kind with a typed payload. The handler receives its own
// payload type, so a module never writes an unmarshal or a type assertion, and a
// malformed payload surfaces as an error the retry machinery can see rather than
// a panic inside the worker loop.
func Define[P any](kind string, schedulable bool, maxAttempts int, h func(context.Context, P) error) Definition {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	return Definition{
		Kind:        kind,
		Schedulable: schedulable,
		MaxAttempts: maxAttempts,
		Handle: func(ctx context.Context, raw json.RawMessage) error {
			var payload P
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &payload); err != nil {
					return fmt.Errorf("job %s payload: %w", kind, err)
				}
			}
			return h(ctx, payload)
		},
	}
}
