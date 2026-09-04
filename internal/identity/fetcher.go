package identity

import "context"

// UserProfile is the provider-neutral mirror of an upstream user record.
type UserProfile struct{ Email, Name, AvatarURL string }

// UserFetcher reads one upstream profile by provider subject.
type UserFetcher interface {
	Fetch(context.Context, string) (UserProfile, error)
}
