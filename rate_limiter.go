package main

import (
	"sync"
	"time"
)

// RateLimiter implements a thread-safe token bucket rate limiter.
type RateLimiter struct {
	mu       sync.Mutex
	rate     float64   // tokens per second
	capacity float64   // maximum burst size
	tokens   float64   // available tokens
	lastTime time.Time // last time tokens were updated
	enabled  bool      // true if limiting is active
}

// NewRateLimiter creates a RateLimiter allowing ratePerMinute events per minute with burst capacity.
// If ratePerMinute <= 0, rate limiting is disabled and all events are permitted.
func NewRateLimiter(ratePerMinute int) *RateLimiter {
	if ratePerMinute <= 0 {
		return &RateLimiter{enabled: false}
	}

	capFloat := float64(ratePerMinute)
	ratePerSec := capFloat / 60.0

	return &RateLimiter{
		rate:     ratePerSec,
		capacity: capFloat,
		tokens:   capFloat,
		lastTime: time.Now(),
		enabled:  true,
	}
}

// Allow returns true if a single event is permitted under the rate limit.
func (rl *RateLimiter) Allow() bool {
	return rl.AllowN(time.Now(), 1)
}

// AllowN checks if n tokens are available at given time 'now'.
func (rl *RateLimiter) AllowN(now time.Time, n float64) bool {
	if rl == nil || !rl.enabled {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Calculate tokens added since last check
	elapsed := now.Sub(rl.lastTime).Seconds()
	if elapsed > 0 {
		rl.tokens += elapsed * rl.rate
		if rl.tokens > rl.capacity {
			rl.tokens = rl.capacity
		}
		rl.lastTime = now
	}

	if rl.tokens >= n {
		rl.tokens -= n
		return true
	}

	return false
}

// Capacity returns the maximum burst capacity.
func (rl *RateLimiter) Capacity() int {
	if rl == nil || !rl.enabled {
		return 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return int(rl.capacity)
}

// Tokens returns the current number of available tokens (for inspection and tests).
func (rl *RateLimiter) Tokens() float64 {
	if rl == nil || !rl.enabled {
		return 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.tokens
}
