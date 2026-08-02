package billing

import (
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
)

func TestMRR(t *testing.T) {
	assert.Equal(t, 0, MRR(nil))
	rows := []sqlc.ListRevenueSubscriptionsRow{
		{ProductKey: "pro", N: 3},   // 3 × $20
		{ProductKey: "team", N: 2},  // 2 × $50
		{ProductKey: "free", N: 10}, // free never counts
	}
	assert.Equal(t, 160, MRR(rows))
}
