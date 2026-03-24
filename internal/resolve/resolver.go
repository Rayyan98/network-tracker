package resolve

import (
	"context"
	"net"
	"sync"
	"time"
)

// Resolver performs cached reverse DNS lookups.
type Resolver struct {
	mu    sync.RWMutex
	cache map[string]cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	hostname string
	expires  time.Time
}

func NewResolver() *Resolver {
	return &Resolver{
		cache: make(map[string]cacheEntry),
		ttl:   10 * time.Minute,
	}
}

// Lookup returns the hostname for an IP, or the IP string if lookup fails.
func (r *Resolver) Lookup(ip string) string {
	r.mu.RLock()
	if e, ok := r.cache[ip]; ok && time.Now().Before(e.expires) {
		r.mu.RUnlock()
		return e.hostname
	}
	r.mu.RUnlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	hostname := ip
	if err == nil && len(names) > 0 {
		// Remove trailing dot
		h := names[0]
		if len(h) > 0 && h[len(h)-1] == '.' {
			h = h[:len(h)-1]
		}
		hostname = h
	}

	r.mu.Lock()
	r.cache[ip] = cacheEntry{hostname: hostname, expires: time.Now().Add(r.ttl)}
	r.mu.Unlock()

	return hostname
}

// StartCleanup periodically removes expired entries.
func (r *Resolver) StartCleanup(done <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			r.mu.Lock()
			for k, v := range r.cache {
				if now.After(v.expires) {
					delete(r.cache, k)
				}
			}
			r.mu.Unlock()
		case <-done:
			return
		}
	}
}
