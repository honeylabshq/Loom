package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/StefanGrimminck/Loom/internal/auth"
	"github.com/StefanGrimminck/Loom/internal/config"
	"github.com/StefanGrimminck/Loom/internal/enrich"
	"github.com/StefanGrimminck/Loom/internal/ingest"
	"github.com/StefanGrimminck/Loom/internal/output"
	"github.com/StefanGrimminck/Loom/internal/ratelimit"
	"github.com/StefanGrimminck/Loom/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

func main() {
	configPath := flag.String("config", "loom.toml", "Path to config file (TOML)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		// Don't log token or config content
		os.Stderr.WriteString("config: " + err.Error() + "\n")
		os.Exit(1)
	}

	// Structured logging; do not log full request bodies or tokens
	logLevel := zerolog.InfoLevel
	switch cfg.Logging.Level {
	case "debug":
		logLevel = zerolog.DebugLevel
	case "warn":
		logLevel = zerolog.WarnLevel
	case "error":
		logLevel = zerolog.ErrorLevel
	}
	zerolog.SetGlobalLevel(logLevel)
	var log zerolog.Logger
	if cfg.Logging.Format == "console" {
		log = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	} else {
		log = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}

	validator := auth.NewValidator(cfg.Auth.Tokens)
	rateLimiter := ratelimit.NewPerSensorLimiter(cfg.Limits.PerSensorRPS)

	// Enrichment: optional GeoIP and ASN DBs
	var dnsEnricher *enrich.DNSEnricher
	if cfg.Enrichment.DNS.Enabled {
		ttl := cfg.Enrichment.DNS.CacheTTL
		if ttl <= 0 {
			ttl = 300
		}
		dnsEnricher = enrich.NewDNSEnricher(
			time.Duration(ttl)*time.Second,
			cfg.Enrichment.DNS.MaxQPS,
		)
	}
	enricher, err := enrich.NewEnricher(
		cfg.Enrichment.GeoIPDBPath,
		cfg.Enrichment.ASNDBPath,
		dnsEnricher,
		log,
	)
	if err != nil {
		log.Fatal().Err(err).Msg("enricher")
	}
	defer func() {
		if err := enricher.Close(); err != nil {
			log.Warn().Err(err).Msg("enricher close")
		}
	}()

	out, err := output.NewWriter(output.WriterConfig{
		Type:                 cfg.Output.Type,
		ElasticsearchURL:     cfg.Output.ElasticsearchURL,
		ElasticsearchIndex:   cfg.Output.ElasticsearchIndex,
		ElasticsearchUser:    cfg.Output.ElasticsearchUser,
		ElasticsearchPass:    cfg.Output.ElasticsearchPass,
		ClickHouseURL:        cfg.Output.ClickHouseURL,
		ClickHouseDatabase:   cfg.Output.ClickHouseDatabase,
		ClickHouseTable:      cfg.Output.ClickHouseTable,
		ClickHouseUser:       cfg.Output.ClickHouseUser,
		ClickHousePassword:   cfg.Output.ClickHousePassword,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("output")
	}
	defer func() {
		if err := out.Close(); err != nil {
			log.Warn().Err(err).Msg("output close")
		}
	}()

	var metricsHandler http.Handler
	var ingestMetrics *ingest.Metrics
	if cfg.Observability.MetricsEnabled {
		promReg := prometheus.NewRegistry()
		metricsHandler = promhttp.HandlerFor(promReg, promhttp.HandlerOpts{})
		ingestMetrics = ingest.NewMetrics(promReg)
	}

	ingestHandler := &ingest.Handler{
		Validator:     validator,
		RateLimiter:   rateLimiter,
		MaxBodyBytes:  cfg.Limits.MaxBodySizeBytes,
		MaxEvents:     cfg.Limits.MaxEventsPerBatch,
		MaxEventBytes: cfg.Limits.MaxEventSizeBytes,
		ProcessBatch: func(sensorID string, events []map[string]interface{}) error {
			for _, ev := range events {
				enricher.EnrichEvent(ev)
				if err := out.Write(ev); err != nil {
					return err
				}
			}
			return nil
		},
		Log:     log,
		Metrics: ingestMetrics,
	}

	var tlsConfig *tls.Config
	if cfg.Server.TLS && (cfg.Server.CertFile != "" && cfg.Server.KeyFile != "") {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	srv := &server.Server{
		IngestHandler:  ingestHandler,
		EnricherReady:  enricher.Ready,
		OutputReady:    func() bool { return true },
		MetricsHandler: metricsHandler,
		Logger:         log,
		TLSConfig:      tlsConfig,
		CertFile:       cfg.Server.CertFile,
		KeyFile:        cfg.Server.KeyFile,
		ListenAddr:     cfg.Server.ListenAddress,
		ManagementAddr: cfg.Server.ManagementListenAddress,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Run(ctx); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("server")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutting down")
}
