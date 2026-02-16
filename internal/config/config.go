package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all Loom configuration.
type Config struct {
	Server        ServerConfig        `toml:"server"`
	Auth          AuthConfig          `toml:"auth"`
	Limits        LimitsConfig        `toml:"limits"`
	Enrichment    EnrichmentConfig    `toml:"enrichment"`
	Output        OutputConfig        `toml:"output"`
	Logging       LoggingConfig       `toml:"logging"`
	Observability ObservabilityConfig `toml:"observability"`
}

type ServerConfig struct {
	ListenAddress          string `toml:"listen_address"`
	TLS                    bool   `toml:"tls"`
	CertFile               string `toml:"cert_file"`
	KeyFile                string `toml:"key_file"`
	ManagementListenAddress string `toml:"management_listen_address"`
}

type AuthConfig struct {
	TokenFile string            `toml:"token_file"`
	Tokens    map[string]string `toml:"tokens"`
}

type LimitsConfig struct {
	MaxBodySizeBytes   int64 `toml:"max_body_size_bytes"`
	MaxEventsPerBatch  int   `toml:"max_events_per_batch"`
	MaxEventSizeBytes  int64 `toml:"max_event_size_bytes"`
	PerSensorRPS       int   `toml:"per_sensor_rps"`
	PerSensorEventsRPS int   `toml:"per_sensor_events_rps"`
}

type EnrichmentConfig struct {
	GeoIPDBPath string     `toml:"geoip_db_path"`
	ASNDBPath   string     `toml:"asn_db_path"`
	DNS         DNSConfig  `toml:"dns"`
}

type DNSConfig struct {
	Enabled      bool   `toml:"enabled"`
	ResolverAddr string `toml:"resolver_addr"`
	CacheTTL     int    `toml:"cache_ttl_seconds"`
	MaxQPS       int    `toml:"max_qps"`
}

type OutputConfig struct {
	Type                 string   `toml:"type"`
	ElasticsearchURL     string   `toml:"elasticsearch_url"`
	ElasticsearchIndex   string   `toml:"elasticsearch_index"`
	ElasticsearchUser    string   `toml:"elasticsearch_user"`
	ElasticsearchPass    string   `toml:"elasticsearch_pass"`
	ClickHouseURL        string   `toml:"clickhouse_url"`
	ClickHouseDatabase   string   `toml:"clickhouse_database"`
	ClickHouseTable      string   `toml:"clickhouse_table"`
	ClickHouseUser       string   `toml:"clickhouse_user"`
	ClickHousePassword   string   `toml:"clickhouse_password"`
	KafkaBrokers         []string `toml:"kafka_brokers"`
	KafkaTopic           string   `toml:"kafka_topic"`
}

type LoggingConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

type ObservabilityConfig struct {
	MetricsEnabled bool `toml:"metrics_enabled"`
}

// Load reads config from path (TOML) and applies environment overrides (secrets).
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if _, err := toml.Decode(string(data), &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.setDefaults()
	if err := c.applyEnv(); err != nil {
		return nil, err
	}
	return &c, c.validate()
}

func (c *Config) setDefaults() {
	if c.Server.ListenAddress == "" {
		c.Server.ListenAddress = ":8443"
	}
	// TLS default is left to config; production should set tls: true and cert_file/key_file
	if c.Limits.MaxBodySizeBytes == 0 {
		c.Limits.MaxBodySizeBytes = 2 * 1024 * 1024 // 2 MiB
	}
	if c.Limits.MaxEventsPerBatch == 0 {
		c.Limits.MaxEventsPerBatch = 500
	}
	if c.Limits.MaxEventSizeBytes == 0 {
		c.Limits.MaxEventSizeBytes = 128 * 1024
	}
	if c.Limits.PerSensorRPS == 0 {
		c.Limits.PerSensorRPS = 50
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Auth.Tokens == nil {
		c.Auth.Tokens = make(map[string]string)
	}
}

func (c *Config) applyEnv() error {
	// Tokens: LOOM_SENSOR_<sensor_id>=<token> (sensor_id from env key, token from value)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "LOOM_SENSOR_") {
			continue
		}
		key, val, _ := strings.Cut(e, "=")
		if val == "" {
			continue
		}
		sensorID := strings.TrimPrefix(key, "LOOM_SENSOR_")
		sensorID = strings.ReplaceAll(sensorID, "_", "-") // allow env-friendly names
		c.Auth.Tokens[val] = sensorID
	}
	// Token file: lines of "token,sensor_id"
	if c.Auth.TokenFile != "" {
		data, err := os.ReadFile(c.Auth.TokenFile)
		if err != nil {
			return fmt.Errorf("auth token_file: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			token, sensorID, ok := strings.Cut(line, ",")
			if !ok {
				continue
			}
			token = strings.TrimSpace(token)
			sensorID = strings.TrimSpace(sensorID)
			if token != "" && sensorID != "" {
				c.Auth.Tokens[token] = sensorID
			}
		}
	}
	// Elasticsearch credentials from env
	if u := os.Getenv("LOOM_ELASTICSEARCH_USER"); u != "" {
		c.Output.ElasticsearchUser = u
	}
	if p := os.Getenv("LOOM_ELASTICSEARCH_PASS"); p != "" {
		c.Output.ElasticsearchPass = p
	}
	if u := os.Getenv("LOOM_CLICKHOUSE_USER"); u != "" {
		c.Output.ClickHouseUser = u
	}
	if p := os.Getenv("LOOM_CLICKHOUSE_PASSWORD"); p != "" {
		c.Output.ClickHousePassword = p
	}
	return nil
}

func (c *Config) validate() error {
	if c.Server.TLS {
		if c.Server.CertFile == "" || c.Server.KeyFile == "" {
			return fmt.Errorf("server: tls enabled but cert_file or key_file missing")
		}
		if _, err := os.Stat(c.Server.CertFile); err != nil {
			return fmt.Errorf("server: cert_file %q not readable: %w", c.Server.CertFile, err)
		}
		if _, err := os.Stat(c.Server.KeyFile); err != nil {
			return fmt.Errorf("server: key_file %q not readable: %w", c.Server.KeyFile, err)
		}
	}
	if len(c.Auth.Tokens) == 0 {
		return fmt.Errorf("auth: no tokens configured (use token_file or LOOM_SENSOR_* env)")
	}
	// One token per sensor: each token must map to exactly one sensor
	seenSensor := make(map[string]string)
	for token, sensorID := range c.Auth.Tokens {
		if prev, ok := seenSensor[sensorID]; ok && prev != token {
			return fmt.Errorf("auth: sensor %q has multiple tokens", sensorID)
		}
		seenSensor[sensorID] = token
	}
	if c.Output.Type == "" {
		c.Output.Type = "stdout"
	}
	if c.Output.Type != "stdout" && c.Output.Type != "elasticsearch" && c.Output.Type != "kafka" && c.Output.Type != "clickhouse" {
		return fmt.Errorf("output: unknown type %q", c.Output.Type)
	}
	if c.Output.Type == "elasticsearch" && c.Output.ElasticsearchURL == "" {
		return fmt.Errorf("output: elasticsearch_url required when type=elasticsearch")
	}
	if c.Output.Type == "clickhouse" && c.Output.ClickHouseURL == "" {
		return fmt.Errorf("output: clickhouse_url required when type=clickhouse")
	}
	return nil
}

// TokenToSensor returns the sensor ID for a token, or "" if invalid. Used after Load.
func (c *Config) TokenToSensor(token string) string {
	return c.Auth.Tokens[token]
}

// HasToken performs constant-time token lookup (we still need to compare constant-time).
func (c *Config) HasToken(token string) bool {
	_, ok := c.Auth.Tokens[token]
	return ok
}

// SensorIDForToken returns sensor id if token is valid (constant-time compare in caller).
func (c *Config) SensorIDForToken(token string) (sensorID string, ok bool) {
	sensorID, ok = c.Auth.Tokens[token]
	return sensorID, ok
}

// EnvInt returns an int from env or default.
func EnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}
