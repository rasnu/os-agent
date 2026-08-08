// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package ratelimit

import (
	"sync"
	"time"
)

// Limiter implements a thread-safe token-bucket rate limiter.
type Limiter struct {
	mu       sync.Mutex
	rate     float64
	capacity float64
	tokens   float64
	lastTime time.Time
	enabled  bool
}

// New creates a Limiter allowing ratePerMinute events per minute with a matching burst capacity.
// Values <= 0 disable rate limiting.
func New(ratePerMinute int) *Limiter {
	if ratePerMinute <= 0 {
		return &Limiter{enabled: false}
	}

	capFloat := float64(ratePerMinute)
	ratePerSec := capFloat / 60.0

	return &Limiter{
		rate:     ratePerSec,
		capacity: capFloat,
		tokens:   capFloat,
		lastTime: time.Now(),
		enabled:  true,
	}
}

func (rl *Limiter) Allow() bool {
	return rl.AllowN(time.Now(), 1)
}

func (rl *Limiter) AllowN(now time.Time, n float64) bool {
	if rl == nil || !rl.enabled {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

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

func (rl *Limiter) Capacity() int {
	if rl == nil || !rl.enabled {
		return 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return int(rl.capacity)
}

func (rl *Limiter) Tokens() float64 {
	if rl == nil || !rl.enabled {
		return 0
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.tokens
}
