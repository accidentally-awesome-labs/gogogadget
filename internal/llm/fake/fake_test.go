package fake

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/llm"
	"testing"
)

func TestFakeCompletion(t *testing.T) {
	got, err := (Completer{}).Chat(context.Background(), llm.ChatRequest{Messages: []llm.Message{{Role: "user", Content: "hi"}}})
	if err != nil || got.Model != "fake" {
		t.Fatalf("fake completion: %v %#v", err, got)
	}
}
