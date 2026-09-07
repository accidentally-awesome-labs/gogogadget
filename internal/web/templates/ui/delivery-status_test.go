package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// DeliveryState is a closed enum this module declares, so its normalization and
// its declared set are exercised here rather than in ui-core's enums_test.go.
// Naming it there put a symbol ggg/component/delivery-status owns in the payload
// every closure installs, and a project without a delivery status then wrote a
// tree whose tests did not compile.
//
// The default is a judgement: an unrecognised delivery state must never read as
// delivered, because claiming a webhook arrived when nobody knows is the one
// wrong answer.
func TestDeliveryStateNormalizesToPending(t *testing.T) {
	assert.Equal(t, DeliveryPending, DeliveryState("").Value(),
		"an unrecognised delivery state must never read as delivered: claiming "+
			"a webhook arrived when nobody knows is the one wrong answer")
	assert.Equal(t, DeliveryPending, DeliveryState("bounced").Value())
}

// A declared value must survive normalization untouched, and Valid must agree
// with the declared set.
func TestDeclaredDeliveryStatesRoundTrip(t *testing.T) {
	for _, v := range DeliveryStates {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	assertDistinct(t, "DeliveryStates", DeliveryStates)
}
