package auth

import (
	"testing"
)

func TestValidator_Validate(t *testing.T) {
	tokenToSensor := map[string]string{
		"secret-token-1": "spip-001",
		"secret-token-2": "vps-frankfurt-01",
	}
	v := NewValidator(tokenToSensor)

	tests := []struct {
		name     string
		token    string
		wantID   string
	}{
		{"valid token 1", "secret-token-1", "spip-001"},
		{"valid token 2", "secret-token-2", "vps-frankfurt-01"},
		{"empty token", "", ""},
		{"unknown token", "wrong-token", ""},
		{"substring token", "secret-token-1x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.Validate(tt.token)
			if got != tt.wantID {
				t.Errorf("Validate(%q) = %q, want %q", tt.token, got, tt.wantID)
			}
		})
	}
}

func TestValidator_Update(t *testing.T) {
	v := NewValidator(map[string]string{"old": "sensor-a"})
	if v.Validate("old") != "sensor-a" {
		t.Fatal("initial token should work")
	}

	v.Update(map[string]string{"new": "sensor-b"})
	if v.Validate("old") != "" {
		t.Error("old token should be invalid after Update")
	}
	if v.Validate("new") != "sensor-b" {
		t.Error("new token should work after Update")
	}
}
