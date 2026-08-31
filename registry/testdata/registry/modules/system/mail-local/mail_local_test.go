package maillocal

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/mail"
)

func TestExampleMailLocalDelivers(t *testing.T) {
	if err := (sender{}).Send(context.Background(), mail.Message{To: "local@example.com"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}
