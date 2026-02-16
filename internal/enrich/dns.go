package enrich

import (
	"net"
	"sync"
	"time"
)

// DNSEnricher performs reverse DNS (PTR) lookups with in-memory cache and rate limiting.
type DNSEnricher struct {
	cache     map[string]cacheEntry
	cacheTTL  time.Duration
	maxQPS    int
	qpsTicker time.Time
	qpsCount  int
	mu        sync.Mutex
}

type cacheEntry struct {
	name string
	exp  time.Time
}

// NewDNSEnricher creates a PTR enricher. cacheTTL and maxQPS from config.
func NewDNSEnricher(cacheTTL time.Duration, maxQPS int) *DNSEnricher {
	if maxQPS <= 0 {
		maxQPS = 10
	}
	return &DNSEnricher{
		cache:    make(map[string]cacheEntry),
		cacheTTL: cacheTTL,
		maxQPS:   maxQPS,
	}
}

// LookupPTR returns the PTR name for ip, from cache or lookup, rate-limited. Empty string if none.
func (d *DNSEnricher) LookupPTR(ip net.IP) string {
	key := ip.String()
	d.mu.Lock()
	if e, ok := d.cache[key]; ok && time.Now().Before(e.exp) {
		d.mu.Unlock()
		return e.name
	}
	now := time.Now()
	if now.Sub(d.qpsTicker) >= time.Second {
		d.qpsTicker = now
		d.qpsCount = 0
	}
	if d.qpsCount >= d.maxQPS {
		d.mu.Unlock()
		return ""
	}
	d.qpsCount++
	d.mu.Unlock()

	ptr, err := net.LookupAddr(key)
	if err != nil || len(ptr) == 0 {
		d.mu.Lock()
		d.cache[key] = cacheEntry{name: "", exp: now.Add(d.cacheTTL)}
		d.mu.Unlock()
		return ""
	}
	name := ptr[0]
	if len(name) > 0 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
	}
	d.mu.Lock()
	d.cache[key] = cacheEntry{name: name, exp: now.Add(d.cacheTTL)}
	d.mu.Unlock()
	return name
}
