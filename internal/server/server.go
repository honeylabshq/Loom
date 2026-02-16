package server

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/StefanGrimminck/Loom/internal/ingest"
	"github.com/rs/zerolog"
)

// Server runs the ingest API and optional management (health, metrics).
type Server struct {
	IngestHandler  http.Handler
	EnricherReady  func() bool
	OutputReady    func() bool
	MetricsHandler http.Handler
	Logger         zerolog.Logger
	TLSConfig      *tls.Config
	CertFile       string
	KeyFile        string
	ListenAddr     string
	ManagementAddr string
}

// Run starts the ingest server (HTTPS) and optionally management server (HTTP on separate port).
func (s *Server) Run(ctx context.Context) error {
	ingestRouter := chi.NewRouter()
	ingestRouter.Use(middleware.RealIP, middleware.Recoverer, requestLogger(s.Logger))
	// Ingest: multiple paths accepted (/api/v1/ingest, /ingest, /) for client flexibility
	ingestRouter.Post("/api/v1/ingest", s.IngestHandler.ServeHTTP)
	ingestRouter.Post("/ingest", s.IngestHandler.ServeHTTP)
	ingestRouter.Post("/", s.IngestHandler.ServeHTTP)

	ingestSrv := &http.Server{
		Addr:              s.ListenAddr,
		Handler:            ingestRouter,
		TLSConfig:          s.tlsConfig(),
		ReadTimeout:        30 * time.Second,
		ReadHeaderTimeout:  10 * time.Second,
		WriteTimeout:       60 * time.Second,
		IdleTimeout:        120 * time.Second,
	}

	if s.ManagementAddr != "" {
		mgmt := chi.NewRouter()
		mgmt.Get("/health", s.serveLiveness)
		mgmt.Get("/live", s.serveLiveness)
		mgmt.Get("/ready", s.serveReadiness)
		if s.MetricsHandler != nil {
			mgmt.Handle("/metrics", s.MetricsHandler)
		}
		mgmtSrv := &http.Server{
			Addr:              s.ManagementAddr,
			Handler:           mgmt,
			ReadTimeout:       5 * time.Second,
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      5 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
		go func() {
			s.Logger.Info().Str("addr", s.ManagementAddr).Msg("management server listening")
			_ = mgmtSrv.ListenAndServe()
		}()
		defer func() {
			mgmtCtx, mgmtCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer mgmtCancel()
			_ = mgmtSrv.Shutdown(mgmtCtx)
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if s.CertFile != "" && s.KeyFile != "" {
			s.Logger.Info().Str("addr", s.ListenAddr).Msg("ingest server (HTTPS) listening")
			errCh <- ingestSrv.ListenAndServeTLS(s.CertFile, s.KeyFile)
		} else {
			s.Logger.Info().Str("addr", s.ListenAddr).Msg("ingest server listening (no TLS)")
			errCh <- ingestSrv.ListenAndServe()
		}
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := ingestSrv.Shutdown(shutdownCtx); err != nil {
			s.Logger.Warn().Err(err).Msg("ingest server shutdown")
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) serveLiveness(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) serveReadiness(w http.ResponseWriter, r *http.Request) {
	if s.EnricherReady != nil && !s.EnricherReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("enricher not ready"))
		return
	}
	if s.OutputReady != nil && !s.OutputReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("output not ready"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func requestLogger(log zerolog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			log.Debug().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", ww.Status()).
				Dur("duration", time.Since(start)).
				Msg("request")
		})
	}
}

// IngestHandler is the interface used by Server for the ingest endpoint.
type IngestHandler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

func (s *Server) tlsConfig() *tls.Config {
	if s.TLSConfig != nil {
		return s.TLSConfig
	}
	if s.CertFile != "" && s.KeyFile != "" {
		return &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return nil
}

// Ensure ingest.Handler implements IngestHandler
var _ IngestHandler = (*ingest.Handler)(nil)
