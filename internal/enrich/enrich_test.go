package enrich

import (
	"testing"

	"github.com/rs/zerolog"
)

// Enricher with no DBs: preserves Spip events and does not add as/geo (no lookups).
func TestEnricher_NoDBs_PreservesEvent(t *testing.T) {
	e, err := NewEnricher("", "", nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	// Spip-style event with source.ip
	ev := map[string]interface{}{
		"@timestamp": "2026-02-15T19:47:09Z",
		"event":      map[string]interface{}{"id": "abc", "ingested_by": "spip"},
		"source":     map[string]interface{}{"ip": "8.8.8.8", "port": float64(12345)},
		"destination": map[string]interface{}{"ip": "10.0.0.1", "port": float64(443)},
		"observer":   map[string]interface{}{"hostname": "spip-001"},
	}
	e.EnrichEvent(ev)

	if ev["@timestamp"] != "2026-02-15T19:47:09Z" {
		t.Error("@timestamp should be preserved")
	}
	src, _ := ev["source"].(map[string]interface{})
	if src == nil || src["ip"] != "8.8.8.8" {
		t.Error("source.ip should be preserved")
	}
	if _, ok := src["as"]; ok {
		t.Error("no ASN DB: source.as should not be added")
	}
	if _, ok := src["geo"]; ok {
		t.Error("no Geo DB: source.geo should not be added")
	}
}

func TestEnricher_NoDBs_MissingSourceIP_PreservesEvent(t *testing.T) {
	e, err := NewEnricher("", "", nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	ev := map[string]interface{}{
		"event":       map[string]interface{}{"id": "x"},
		"destination": map[string]interface{}{"ip": "1.2.3.4"},
	}
	e.EnrichEvent(ev)

	if ev["destination"] == nil {
		t.Error("destination should be preserved")
	}
	// No source.ip: enrichment is skipped; source may be added as empty map by enricher
	src, _ := ev["source"].(map[string]interface{})
	if src != nil && len(src) > 0 {
		t.Error("no source.ip: should not add as/geo")
	}
}

func TestEnricher_NoDBs_NilEvent_NoPanic(t *testing.T) {
	e, err := NewEnricher("", "", nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.EnrichEvent(nil)
}

func TestEnricher_NoDBs_InvalidIP_PreservesEvent(t *testing.T) {
	e, err := NewEnricher("", "", nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	ev := map[string]interface{}{
		"source": map[string]interface{}{"ip": "not-an-ip"},
	}
	e.EnrichEvent(ev)

	if ev["source"] == nil {
		t.Error("event should be preserved")
	}
}

func TestEnricher_Ready(t *testing.T) {
	e, err := NewEnricher("", "", nil, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !e.Ready() {
		t.Error("Ready() should be true even with no DBs")
	}
}
