package llm

import (
	"context"
	"testing"
)

type contractCompleter struct{}

func (contractCompleter) Chat(ctx context.Context, r ChatRequest) (ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{Content: r.Messages[0].Content, Model: "contract"}, nil
}
func TestCompleterContract(t *testing.T) {
	var c Completer = contractCompleter{}
	got, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil || got.Content != "hello" {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
