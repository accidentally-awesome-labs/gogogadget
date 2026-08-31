package s3

import (
	"context"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestS3StoreContract(t *testing.T) {
	var _ storage.Store = (*R2Store)(nil)
	s, err := NewR2Store(context.Background(), "acct", "key", "secret", "bucket", "http://127.0.0.1:1")
	require.NoError(t, err)
	require.NotNil(t, s)
}
