package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MinimalWithEnvToken(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "loom.toml")
	// Minimal TOML: server without TLS, no auth tokens in file (use env)
	content := `
[server]
listen_address = ":8080"
tls = false

[limits]
max_events_per_batch = 100

[output]
type = "stdout"
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Provide token via env so validation passes
	os.Setenv("LOOM_SENSOR_spip01", "test-token")
	defer os.Unsetenv("LOOM_SENSOR_spip01")

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.ListenAddress != ":8080" {
		t.Errorf("listen_address = %q", cfg.Server.ListenAddress)
	}
	if cfg.Server.TLS {
		t.Error("tls should be false")
	}
	if cfg.Limits.MaxEventsPerBatch != 100 {
		t.Errorf("max_events_per_batch = %d", cfg.Limits.MaxEventsPerBatch)
	}
	if cfg.Output.Type != "stdout" {
		t.Errorf("output type = %q", cfg.Output.Type)
	}
	if len(cfg.Auth.Tokens) == 0 {
		t.Error("expected tokens from LOOM_SENSOR_ env")
	}
	if cfg.Auth.Tokens["test-token"] != "spip01" {
		t.Errorf("token should map to spip01, got %q", cfg.Auth.Tokens["test-token"])
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(cfgPath, []byte("invalid toml [[["), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestValidate_NoTokens(t *testing.T) {
	c := &Config{}
	c.setDefaults()
	c.Auth.Tokens = make(map[string]string) // empty
	if err := c.validate(); err == nil {
		t.Fatal("expected validation error when no tokens")
	}
}

func TestValidate_TLSRequiresReadableCertFiles(t *testing.T) {
	c := &Config{}
	c.setDefaults()
	c.Server.TLS = true
	c.Server.CertFile = "/nonexistent/cert.pem"
	c.Server.KeyFile = "/nonexistent/key.pem"
	c.Auth.Tokens = map[string]string{"tk": "s1"}
	if err := c.validate(); err == nil {
		t.Fatal("expected validation error when cert or key file not readable")
	}
}
