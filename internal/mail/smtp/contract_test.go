package smtp

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSMTPModuleContract(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	module, err := NewModule(context.Background(), host, Deps{Config: &config.Config{EmailFrom: "hello@example.com"}})
	require.NoError(t, err)
	require.NotNil(t, module)
	require.NotNil(t, module.Sender)
}

func TestSMTPModuleRejectsMissingConfig(t *testing.T) {
	_, err := NewModule(context.Background(), apphost.Map(nil, time.Now(), "test"), Deps{})
	require.Error(t, err)
}
