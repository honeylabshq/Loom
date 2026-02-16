package ratelimit

import (
	"testing"
	"time"
)

func TestPerSensorLimiter_Allow(t *testing.T) {
	// 2 requests per second per sensor
	limiter := NewPerSensorLimiter(2)

	sensor := "spip-001"
	if !limiter.Allow(sensor) {
		t.Error("first request should be allowed")
	}
	if !limiter.Allow(sensor) {
		t.Error("second request should be allowed")
	}
	if limiter.Allow(sensor) {
		t.Error("third request in same second should be denied")
	}
}

func TestPerSensorLimiter_Allow_DifferentSensors(t *testing.T) {
	limiter := NewPerSensorLimiter(1)

	if !limiter.Allow("sensor-a") {
		t.Error("sensor-a first should be allowed")
	}
	if !limiter.Allow("sensor-b") {
		t.Error("sensor-b first should be allowed (separate bucket)")
	}
	if limiter.Allow("sensor-a") {
		t.Error("sensor-a second should be denied")
	}
}

func TestPerSensorLimiter_Allow_InjectTime(t *testing.T) {
	now := time.Now().UTC().Unix()
	limiter := &PerSensorLimiter{
		rps:      1,
		lastTick: make(map[string]int64),
		count:    make(map[string]int),
		nowFn:    func() time.Time { return time.Unix(now, 0) },
	}

	if !limiter.Allow("x") {
		t.Error("first should be allowed")
	}
	if limiter.Allow("x") {
		t.Error("second in same second should be denied")
	}

	// Next second
	limiter.nowFn = func() time.Time { return time.Unix(now+1, 0) }
	if !limiter.Allow("x") {
		t.Error("first in new second should be allowed")
	}
}

func TestNewPerSensorLimiter_ZeroRPS(t *testing.T) {
	l := NewPerSensorLimiter(0)
	if l.rps != 50 {
		t.Errorf("zero rps should default to 50, got %d", l.rps)
	}
}

func TestNewPerSensorLimiter_NegativeRPS_NoLimit(t *testing.T) {
	l := NewPerSensorLimiter(-1)
	for i := 0; i < 100; i++ {
		if !l.Allow("s") {
			t.Fatalf("with rps=-1, request %d should be allowed", i+1)
		}
	}
}
