package server

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// ipRateLimiter is a per-IP fixed-window rate limiter. It fires before auth so
// unauthenticated floods are rejected before touching the token validator.
type ipRateLimiter struct {
	mu       sync.Mutex
	rps      int
	lastTick map[string]int64
	count    map[string]int
	nowFn    func() time.Time
}

func newIPRateLimiter(rps int) *ipRateLimiter {
	l := &ipRateLimiter{
		rps:      rps,
		lastTick: make(map[string]int64),
		count:    make(map[string]int),
		nowFn:    time.Now,
	}
	go l.cleanup()
	return l
}

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowFn().Unix()
	tick, ok := l.lastTick[ip]
	if !ok || tick != now {
		l.lastTick[ip] = now
		l.count[ip] = 0
	}
	if l.count[ip] >= l.rps {
		return false
	}
	l.count[ip]++
	return true
}

// cleanup removes stale entries every 60 seconds to bound memory usage.
func (l *ipRateLimiter) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().Unix()
		l.mu.Lock()
		for ip, tick := range l.lastTick {
			if now-tick > 5 {
				delete(l.lastTick, ip)
				delete(l.count, ip)
			}
		}
		l.mu.Unlock()
	}
}

func (l *ipRateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(realClientIP(r)) {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate_limit_exceeded"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// realClientIP returns the client IP from r.RemoteAddr, which chi's RealIP
// middleware has already rewritten from X-Real-IP / X-Forwarded-For.
func realClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
