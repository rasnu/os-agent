// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterBurstAndExhaustion(t *testing.T) {
	rl := New(5)

	if rl.Capacity() != 5 {
		t.Fatalf("expected capacity 5, got %d", rl.Capacity())
	}

	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	if rl.Allow() {
		t.Fatalf("expected request 6 to be rejected by rate limiter")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := New(60)

	for i := 0; i < 60; i++ {
		if !rl.Allow() {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	if rl.Allow() {
		t.Fatalf("expected request 61 to be rejected")
	}

	now := rl.lastTime.Add(2 * time.Second)
	if !rl.AllowN(now, 1) {
		t.Fatalf("expected request to be allowed after 2s refill")
	}

	if tokens := rl.Tokens(); tokens < 0.99 || tokens > 1.01 {
		t.Fatalf("expected ~1 token remaining, got %f", tokens)
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := New(0)
	for i := 0; i < 200; i++ {
		if !rl.Allow() {
			t.Fatalf("expected unlimited requests to be allowed when rate <= 0")
		}
	}
}

func TestRateLimiterConcurrency(t *testing.T) {
	rl := New(100)
	var wg sync.WaitGroup
	var allowedCount int64
	var mu sync.Mutex

	for i := 0; i < 150; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rl.Allow() {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if allowedCount != 100 {
		t.Fatalf("expected exactly 100 requests allowed concurrently, got %d", allowedCount)
	}
}
