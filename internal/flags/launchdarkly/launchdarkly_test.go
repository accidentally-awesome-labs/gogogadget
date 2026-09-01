package launchdarkly

import (
	"context"
	"errors"
	"github.com/gogogadget/gogogadget/internal/flags"
	"testing"
)

func TestMutationsAreReadOnly(t *testing.T) {
	s := New("https://console.example", func(context.Context, string, string) bool { return true }, func(context.Context) ([]flags.Flag, error) { return nil, nil }, func(context.Context, string) ([]flags.Override, error) { return nil, nil })
	for _, err := range []error{s.Upsert(context.Background(), flags.Flag{}), s.Delete(context.Background(), "x"), s.SetOverride(context.Background(), "x", "o", true), s.DeleteOverride(context.Background(), "x", "o")} {
		if !errors.Is(err, flags.ErrReadOnly) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestNewModuleRejectsMissingClient(t *testing.T) {
	_, err := NewModule(context.Background(), nil, Deps{})
	if err == nil {
		t.Fatal("NewModule accepted missing client")
	}
}
