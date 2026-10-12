package main

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterBurstAndExhaustion(t *testing.T) {
	// 5 commands per minute
	rl := NewRateLimiter(5)

	if rl.Capacity() != 5 {
		t.Fatalf("expected capacity 5, got %d", rl.Capacity())
	}

	// First 5 should succeed
	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	// 6th should be rejected immediately
	if rl.Allow() {
		t.Fatalf("expected request 6 to be rejected by rate limiter")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := NewRateLimiter(60) // 1 token per second

	// Exhaust all tokens
	for i := 0; i < 60; i++ {
		if !rl.Allow() {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	// Now rejected
	if rl.Allow() {
		t.Fatalf("expected request 61 to be rejected")
	}

	// Advance time by 2 seconds using AllowN
	now := rl.lastTime.Add(2 * time.Second)
	if !rl.AllowN(now, 1) {
		t.Fatalf("expected request to be allowed after 2s refill")
	}

	// Tokens should have had 2 - 1 = 1 remaining
	if tokens := rl.Tokens(); tokens < 0.99 || tokens > 1.01 {
		t.Fatalf("expected ~1 token remaining, got %f", tokens)
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	rl := NewRateLimiter(0)
	for i := 0; i < 200; i++ {
		if !rl.Allow() {
			t.Fatalf("expected unlimited requests to be allowed when rate <= 0")
		}
	}
}

func TestRateLimiterConcurrency(t *testing.T) {
	rl := NewRateLimiter(100)
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
