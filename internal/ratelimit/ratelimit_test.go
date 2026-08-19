package ratelimit

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestKeyedAllowsBurstThenDenies(t *testing.T) {
	// rate.Limit(0) never refills, so the burst is the whole budget and the
	// test is deterministic — no sleeping, no wall-clock flake.
	kl := NewKeyed(0, 2)
	assert.True(t, kl.Allow("k"), "first request inside burst")
	assert.True(t, kl.Allow("k"), "second request inside burst")
	assert.False(t, kl.Allow("k"), "burst exhausted")
}

func TestKeyedBudgetsAreIndependentPerKey(t *testing.T) {
	kl := NewKeyed(0, 1)
	assert.True(t, kl.Allow("a"))
	assert.False(t, kl.Allow("a"), "a is spent")
	assert.True(t, kl.Allow("b"), "b must not be starved by a — that is the point of keying")
}

func TestPerMinuteBurstIsDoubleTheRate(t *testing.T) {
	kl := PerMinute(3)
	assert.Equal(t, 6, kl.burst)
	assert.InDelta(t, float64(rate.Every(20_000_000_000)), float64(kl.rate), 0.001, "3/min = one token per 20s")
}

func TestKeyedIsConcurrencySafe(t *testing.T) {
	kl := NewKeyed(0, 100)
	var wg sync.WaitGroup
	allowed := make([]bool, 200)
	for i := range allowed {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed[i] = kl.Allow("shared")
		}()
	}
	wg.Wait()
	granted := 0
	for _, ok := range allowed {
		if ok {
			granted++
		}
	}
	assert.Equal(t, 100, granted, "exactly the burst is granted, no matter the interleaving")
}
