package ingest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/StefanGrimminck/Loom/internal/auth"
	"github.com/StefanGrimminck/Loom/internal/ratelimit"
	"github.com/rs/zerolog"
)

// spipStyleEvent returns a minimal ECS event as produced by Spip (see Spip-Go internal/logging).
func spipStyleEvent(sourceIP, sensorName string) map[string]interface{} {
	return map[string]interface{}{
		"@timestamp": "2026-02-15T19:47:09Z",
		"event": map[string]interface{}{
			"id":           "a21c163a-8c63-4001-81db-1d5618357f1a",
			"ingested_by":  "spip",
			"summary":      "GET /.well-known/security.txt",
		},
		"source":      map[string]interface{}{"ip": sourceIP, "port": float64(4496)},
		"destination": map[string]interface{}{"ip": "5.175.183.132", "port": float64(6379)},
		"host":        map[string]interface{}{"name": sensorName},
		"observer":    map[string]interface{}{"hostname": sensorName, "id": sensorName},
		"network":     map[string]interface{}{"transport": "tcp", "protocol": "tls"},
	}
}

func TestHandler_MethodNotAllowed(t *testing.T) {
	h := makeTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/ingest", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_InvalidContentType(t *testing.T) {
	h := makeTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader([]byte("[]")))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Spip-ID", "spip-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rec.Code)
	}
}

func TestHandler_Unauthorized_NoAuth(t *testing.T) {
	h := makeTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader([]byte("[]")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_Unauthorized_InvalidToken(t *testing.T) {
	h := makeTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader([]byte("[]")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("X-Spip-ID", "spip-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestHandler_Unauthorized_XSpipIDMismatch(t *testing.T) {
	h := makeTestHandler(t)
	body := mustJSON([]interface{}{spipStyleEvent("1.2.3.4", "spip-001")})
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Spip-ID", "other-sensor")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (X-Spip-ID must match token)", rec.Code)
	}
}

func TestHandler_BadRequest_NotArray(t *testing.T) {
	h := makeTestHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader([]byte(`{"a":1}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Spip-ID", "spip-001")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_Success_SpipStyleBatch(t *testing.T) {
	var processed []map[string]interface{}
	h := makeTestHandler(t)
	h.ProcessBatch = func(sensorID string, events []map[string]interface{}) error {
		processed = events
		return nil
	}

	batch := []interface{}{
		spipStyleEvent("167.94.146.54", "spip-001"),
		spipStyleEvent("8.8.8.8", "spip-001"),
	}
	body := mustJSON(batch)
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Spip-ID", "spip-001")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if len(processed) != 2 {
		t.Fatalf("ProcessBatch called with %d events, want 2", len(processed))
	}
	if src, _ := processed[0]["source"].(map[string]interface{}); src == nil {
		t.Error("first event missing source")
	} else if src["ip"] != "167.94.146.54" {
		t.Errorf("source.ip = %v", src["ip"])
	}
	if ev, _ := processed[0]["event"].(map[string]interface{}); ev == nil {
		t.Error("first event missing event")
	} else if ev["ingested_by"] != "spip" {
		t.Errorf("event.ingested_by = %v", ev["ingested_by"])
	}
}

func makeTestHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{
		Validator:     auth.NewValidator(map[string]string{"test-token": "spip-001"}),
		RateLimiter:   ratelimit.NewPerSensorLimiter(100),
		MaxBodyBytes:  1024 * 1024,
		MaxEvents:     500,
		MaxEventBytes: 128 * 1024,
		ProcessBatch:  func(string, []map[string]interface{}) error { return nil },
		Log:           zerolog.Nop(),
	}
}

func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
