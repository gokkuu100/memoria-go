package auth

import (
	"sync"
	"time"
)

// Limiter is a simple in-memory sliding-window rate limiter. Sufficient for a
// single API instance; replaced by a shared store if we scale out (B11).
type Limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func NewLimiter() *Limiter {
	return &Limiter{hits: make(map[string][]time.Time)}
}

func (l *Limiter) Allow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	cutoff := now.Add(-window)

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
