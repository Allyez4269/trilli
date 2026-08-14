// Package ratelimit is a small in-process, per-key token-bucket limiter for
// abuse-prone endpoints (login, 2FA completion, password resets, link-password
// attempts). It deliberately has no external dependencies and no persistence:
// a restart resets the buckets, which is acceptable for brute-force protection
// on a single-binary deployment.
//
// Each Limiter holds an independent bucket per key (typically the client IP,
// sometimes ip|email or ip|token). Buckets refill continuously at Rate tokens
// per minute up to Burst. Stale buckets are swept opportunistically so the map
// can't grow without bound under address-spoofing floods.
package ratelimit

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter is a keyed token-bucket rate limiter. Safe for concurrent use.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	rate  float64 // tokens added per second
	burst float64 // bucket capacity (and starting balance)

	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// PerMinute builds a limiter allowing ratePerMin sustained attempts per key
// per minute, with a burst ceiling.
func PerMinute(ratePerMin, burst int) *Limiter {
	if ratePerMin < 1 {
		ratePerMin = 1
	}
	if burst < ratePerMin {
		burst = ratePerMin
	}
	return &Limiter{
		buckets:   make(map[string]*bucket),
		rate:      float64(ratePerMin) / 60.0,
		burst:     float64(burst),
		lastSweep: time.Now(),
	}
}

// Allow reports whether the key may proceed, consuming one token when it can.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Opportunistic sweep: drop buckets that have fully refilled (idle long
	// enough to be indistinguishable from fresh) at most once a minute.
	if now.Sub(l.lastSweep) > time.Minute {
		fullAfter := time.Duration(l.burst/l.rate) * time.Second
		for k, b := range l.buckets {
			if now.Sub(b.last) > fullAfter {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		b.tokens += now.Sub(b.last).Seconds() * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ClientIP extracts the real client IP the same way the auth package does:
// CF-Connecting-IP (set by Cloudflare, not spoofable through it), then the
// LAST X-Forwarded-For entry (appended by our trusted proxy), then RemoteAddr.
// Duplicated here so ratelimit stays dependency-free (auth imports would work
// today but couples the layers needlessly).
func ClientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// TooMany writes the standard 429 response.
func TooMany(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "60")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"Too many attempts. Please wait a minute and try again."}`))
}
