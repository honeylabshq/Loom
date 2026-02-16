package ratelimit

import (
	"sync"
	"time"
)

// PerSensorLimiter enforces per-sensor rate limits (requests per second).
// Returns 429 when the limit is exceeded.
type PerSensorLimiter struct {
	mu       sync.Mutex
	rps      int
	lastTick map[string]int64   // sensor -> last second bucket
	count    map[string]int      // sensor -> count in current second
	nowFn    func() time.Time
}

// NewPerSensorLimiter creates a limiter allowing rps requests per second per sensor.
// If rps is 0, defaults to 50. If rps is negative (e.g. -1), rate limiting is disabled (Allow always returns true).
func NewPerSensorLimiter(rps int) *PerSensorLimiter {
	if rps == 0 {
		rps = 50
	}
	if rps < 0 {
		rps = 0
	}
	return &PerSensorLimiter{
		rps:      rps,
		lastTick: make(map[string]int64),
		count:    make(map[string]int),
		nowFn:    time.Now().UTC,
	}
}

// Allow returns true if the sensor is within rate limit, false otherwise (caller should return 429).
func (p *PerSensorLimiter) Allow(sensorID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.rps <= 0 {
		return true
	}
	now := p.nowFn().Unix()
	tick, ok := p.lastTick[sensorID]
	if !ok || tick != now {
		p.lastTick[sensorID] = now
		p.count[sensorID] = 0
	}
	if p.count[sensorID] >= p.rps {
		return false
	}
	p.count[sensorID]++
	return true
}

// RetryAfterSeconds returns a suggested Retry-After value in seconds when rate limited.
func (p *PerSensorLimiter) RetryAfterSeconds(sensorID string) int {
	return 1
}
