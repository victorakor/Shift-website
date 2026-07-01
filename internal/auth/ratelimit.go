package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter is a simple in-memory sliding-window limiter, keyed by client IP.
// Spec target: max ~5 attempts per 10 minutes per IP on /api/recover.
type RateLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	maxHits  int
	attempts map[string][]time.Time
}

func NewRateLimiter(window time.Duration, maxHits int) *RateLimiter {
	return &RateLimiter{
		window:   window,
		maxHits:  maxHits,
		attempts: map[string][]time.Time{},
	}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rl.window)
	hits := rl.attempts[key]
	kept := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.maxHits {
		rl.attempts[key] = kept
		return false
	}
	kept = append(kept, now)
	rl.attempts[key] = kept
	return true
}

// ClientIP extracts a best-effort client IP for rate-limit keying.
func ClientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
