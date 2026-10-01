package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter is a per-key token-bucket rate limiter with idle eviction.
type Limiter struct {
	mu      sync.Mutex
	r       rate.Limit
	burst   int
	entries map[string]*limEntry
	last    time.Time
}

type limEntry struct {
	l    *rate.Limiter
	seen time.Time
}

func NewLimiter(perMinute float64, burst int) *Limiter {
	return &Limiter{r: rate.Limit(perMinute / 60), burst: burst, entries: map[string]*limEntry{}, last: time.Now()}
}

func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.last) > time.Minute {
		for k, e := range l.entries {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(l.entries, k)
			}
		}
		l.last = now
	}
	e, ok := l.entries[key]
	if !ok {
		if len(l.entries) > 100000 {
			// Under attack with many keys: fail closed for new keys.
			return false
		}
		e = &limEntry{l: rate.NewLimiter(l.r, l.burst)}
		l.entries[key] = e
	}
	e.seen = now
	return e.l.Allow()
}
