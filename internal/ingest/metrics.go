package ingest

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Prometheus metrics for the ingest API.
type Metrics struct {
	RequestsTotal *prometheus.CounterVec
	EventsTotal   *prometheus.CounterVec
}

// NewMetrics creates and registers ingest metrics. Labels must not include tokens or IPs; sensor_id is allowed.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "loom_ingest_requests_total", Help: "Total ingest requests by sensor and status"},
			[]string{"sensor_id", "status"}),
		EventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "loom_ingest_events_total", Help: "Total events received by sensor"},
			[]string{"sensor_id"}),
	}
	if reg != nil {
		reg.MustRegister(m.RequestsTotal, m.EventsTotal)
	}
	return m
}

func (m *Metrics) IncRequests(sensorID string, status int) {
	if m == nil {
		return
	}
	m.RequestsTotal.WithLabelValues(sensorID, statusToString(status)).Inc()
}

func (m *Metrics) AddEvents(sensorID string, n int) {
	if m == nil {
		return
	}
	m.EventsTotal.WithLabelValues(sensorID).Add(float64(n))
}

func statusToString(code int) string {
	switch code {
	case 200:
		return "200"
	case 204:
		return "204"
	case 400:
		return "400"
	case 401:
		return "401"
	case 413:
		return "413"
	case 429:
		return "429"
	case 500:
		return "500"
	case 503:
		return "503"
	default:
		return "other"
	}
}
