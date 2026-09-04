package identity

import (
	"context"
	"net/http"
	"testing"
)

// stubs for the seam's own capability check. The seam holds contracts only,
// so its test double lives here rather than in an adapter.
type stubVerifier struct{}

func (stubVerifier) Verify(context.Context, string) (*ProviderClaims, error) {
	return &ProviderClaims{Provider: "stub", UserSubject: "user_stub"}, nil
}

type stubFetcher struct{}

func (stubFetcher) Fetch(context.Context, string) (UserProfile, error) { return UserProfile{}, nil }

type stubDeleter struct{}

func (stubDeleter) DeleteUser(context.Context, string) error { return nil }

type stubNavigator struct{}

func (stubNavigator) LoginURL(string) string  { return "/login" }
func (stubNavigator) SignupURL(string) string { return "/signup" }
func (stubNavigator) AccountURL() string      { return "/account" }

type stubWebhook struct{}

func (stubWebhook) Verify(context.Context, []byte, http.Header) (Event, error) {
	return Event{}, nil
}

// TestNewModuleRequiresEveryCapability pins the seam's only behavior: it never
// selects or substitutes a provider, so a missing capability is a refusal
// rather than a silent no-op implementation.
func TestNewModuleRequiresEveryCapability(t *testing.T) {
	full := Deps{
		Verifier:  stubVerifier{},
		Fetcher:   stubFetcher{},
		Deleter:   stubDeleter{},
		Navigator: stubNavigator{},
		Webhook:   stubWebhook{},
	}
	if _, err := NewModule(context.Background(), full); err != nil {
		t.Fatalf("complete capability set refused: %v", err)
	}
	for name, drop := range map[string]func(*Deps){
		"verifier":  func(d *Deps) { d.Verifier = nil },
		"fetcher":   func(d *Deps) { d.Fetcher = nil },
		"deleter":   func(d *Deps) { d.Deleter = nil },
		"navigator": func(d *Deps) { d.Navigator = nil },
		"webhook":   func(d *Deps) { d.Webhook = nil },
	} {
		t.Run("missing "+name, func(t *testing.T) {
			d := full
			drop(&d)
			if _, err := NewModule(context.Background(), d); err == nil {
				t.Fatalf("missing %s accepted", name)
			}
		})
	}
}
