package identity

import "context"

// Deleter removes the upstream account behind a provider subject.
type Deleter interface {
	DeleteUser(context.Context, string) error
}
