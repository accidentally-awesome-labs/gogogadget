package mailmanaged

import (
	"context"
	"testing"

	"github.com/gogogadget/gogogadget/internal/mail"
)

func TestExampleMailManagedDelivers(t *testing.T) {
	if err := (sender{}).Send(context.Background(), mail.Message{To: "managed@example.com"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
}
