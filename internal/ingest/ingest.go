package ingest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/StefanGrimminck/Loom/internal/auth"
	"github.com/StefanGrimminck/Loom/internal/ratelimit"
	"github.com/rs/zerolog"
)

// Handler handles POST ingest requests (JSON array of ECS events).
type Handler struct {
	Validator     *auth.Validator
	RateLimiter   *ratelimit.PerSensorLimiter
	MaxBodyBytes  int64
	MaxEvents     int
	MaxEventBytes int64
	ProcessBatch  func(sensorID string, events []map[string]interface{}) error
	Log           zerolog.Logger
	Metrics       *Metrics
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method_not_allowed"}`))
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte(`{"error":"invalid_content_type"}`))
		return
	}

	// Bearer token validation
	authz := r.Header.Get("Authorization")
	if authz == "" || !strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		if h.Metrics != nil {
			h.Metrics.IncRequests("unknown", http.StatusUnauthorized)
		}
		h.respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer"))
	token = strings.TrimPrefix(token, "bearer ")
	sensorID := h.Validator.Validate(token)
	if sensorID == "" {
		if h.Metrics != nil {
			h.Metrics.IncRequests("unknown", http.StatusUnauthorized)
		}
		h.respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// X-Spip-ID must match the sensor for this token (one token per sensor)
	headerSensorID := r.Header.Get("X-Spip-ID")
	if headerSensorID != "" && headerSensorID != sensorID {
		h.respondErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if headerSensorID == "" {
		headerSensorID = sensorID
	}

	// Per-sensor rate limit
	if !h.RateLimiter.Allow(headerSensorID) {
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusTooManyRequests)
		}
		w.Header().Set("Retry-After", "1")
		h.respondErr(w, http.StatusTooManyRequests, "rate_limit_exceeded")
		return
	}

	// Body size limit
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			if h.Metrics != nil {
				h.Metrics.IncRequests(headerSensorID, http.StatusRequestEntityTooLarge)
			}
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"payload_too_large"}`))
			return
		}
		h.Log.Debug().Err(err).Msg("read body")
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusBadRequest)
		}
		h.respondErr(w, http.StatusBadRequest, "invalid_request")
		return
	}

	// Request body must be a JSON array
	bodyTrim := strings.TrimSpace(string(body))
	if bodyTrim == "" || bodyTrim[0] != '[' {
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusBadRequest)
		}
		h.respondErr(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var events []map[string]interface{}
	if err := json.Unmarshal(body, &events); err != nil {
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusBadRequest)
		}
		h.respondErr(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if events == nil {
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusBadRequest)
		}
		h.respondErr(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if len(events) > h.MaxEvents {
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusRequestEntityTooLarge)
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"batch_too_large"}`))
		return
	}
	for i := range events {
		if events[i] == nil {
			if h.Metrics != nil {
				h.Metrics.IncRequests(headerSensorID, http.StatusBadRequest)
			}
			h.respondErr(w, http.StatusBadRequest, "invalid_request")
			return
		}
		b, _ := json.Marshal(events[i])
		if int64(len(b)) > h.MaxEventBytes {
			if h.Metrics != nil {
				h.Metrics.IncRequests(headerSensorID, http.StatusRequestEntityTooLarge)
			}
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":"event_too_large"}`))
			return
		}
	}

	if h.Metrics != nil {
		h.Metrics.IncRequests(headerSensorID, http.StatusOK)
		h.Metrics.AddEvents(headerSensorID, len(events))
	}

	// Process (enrich + output)
	if err := h.ProcessBatch(headerSensorID, events); err != nil {
		h.Log.Error().Err(err).Str("sensor_id", headerSensorID).Msg("process batch")
		if h.Metrics != nil {
			h.Metrics.IncRequests(headerSensorID, http.StatusInternalServerError)
		}
		h.respondErr(w, http.StatusInternalServerError, "internal_error")
		return
	}

	h.Log.Info().Str("sensor_id", headerSensorID).Int("events", len(events)).Msg("ingest batch ok")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) respondErr(w http.ResponseWriter, code int, errMsg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + errMsg + `"}`))
}
