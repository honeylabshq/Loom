package auth

import (
	"crypto/subtle"
	"sync"
)

// Validator validates Bearer tokens and returns the single sensor ID (X-Spip-ID) for that token.
// Uses constant-time comparison; one token per sensor.
type Validator struct {
	mu     sync.RWMutex
	tokens []tokenEntry
}

type tokenEntry struct {
	token    []byte
	sensorID string
}

// NewValidator returns a validator that checks tokens in constant time.
func NewValidator(tokenToSensor map[string]string) *Validator {
	v := &Validator{}
	v.Update(tokenToSensor)
	return v
}

// Update replaces the token map (e.g. after config reload). Caller must not pass nil.
func (v *Validator) Update(tokenToSensor map[string]string) {
	entries := make([]tokenEntry, 0, len(tokenToSensor))
	for token, sensorID := range tokenToSensor {
		entries = append(entries, tokenEntry{token: []byte(token), sensorID: sensorID})
	}
	v.mu.Lock()
	v.tokens = entries
	v.mu.Unlock()
}

// Validate returns the sensor ID for the given token if it is valid, or "" otherwise.
// Uses constant-time comparison. MUST NOT log the token.
func (v *Validator) Validate(token string) (sensorID string) {
	if token == "" {
		return ""
	}
	b := []byte(token)
	v.mu.RLock()
	defer v.mu.RUnlock()
	for _, e := range v.tokens {
		if subtle.ConstantTimeCompare(e.token, b) == 1 {
			return e.sensorID
		}
	}
	return ""
}
