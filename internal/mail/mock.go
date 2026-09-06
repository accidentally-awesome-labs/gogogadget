package mail

import (
	"context"
	"sync"
)

// MockSender is the mail seam's own test double: it records every message
// instead of delivering one, and takes an injectable error for the failure
// path. It ships with the seam for the same reason billing.MockClient does —
// a payload of a seam-consuming module must never name an adapter package,
// because an adapter is a per-environment provider selection and a test that
// constructs one compiles only while that selection holds.
//
// It replaces two things: the filesystem dev adapter, which test payloads
// reached for and then read files back off disk, and the local captureSender
// copies that grew beside it. A recorded message is a stronger assertion
// than a file on disk anyway — it carries the subject and both bodies, and
// needs no temp directory, no glob and no cleanup.
type MockSender struct {
	// Err, when non-nil, is returned by Send and the message is NOT
	// recorded: a delivery that failed did not happen.
	Err error

	mu   sync.Mutex
	sent []Message
}

func (s *MockSender) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	s.sent = append(s.sent, msg)
	return nil
}

// Sent returns the delivered messages, in order.
func (s *MockSender) Sent() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Message(nil), s.sent...)
}

// Recipients returns the To address of every delivered message, in order.
func (s *MockSender) Recipients() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.sent))
	for _, m := range s.sent {
		out = append(out, m.To)
	}
	return out
}

var _ Sender = (*MockSender)(nil)
