package ingest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

func fixedClock(v *SensorVersions, start time.Time) func(time.Duration) {
	now := start
	v.now = func() time.Time { return now }
	return func(d time.Duration) { now = now.Add(d) }
}

func TestCleanVersion(t *testing.T) {
	for in, want := range map[string]string{
		"v1.4.0-12-g3d9d463":        "v1.4.0-12-g3d9d463",
		" 3d9d463 ":                 "3d9d463",
		"3d9d463-dirty":             "3d9d463-dirty",
		"":                          "",
		"-leading-dash":             "",
		"has space":                 "",
		"quote\"":                   "",
		"new\nline":                 "",
		strings.Repeat("a", 64):     strings.Repeat("a", 64),
		strings.Repeat("a", 65):     "",
		"v1.0.0+build.5":            "v1.0.0+build.5",
		"<script>alert(1)</script>": "",
	} {
		if got := CleanVersion(in); got != want {
			t.Errorf("CleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSensorVersions_ChangesAndSince(t *testing.T) {
	v := NewSensorVersions("", nil, zerolog.Nop())
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	tick := fixedClock(v, t0)

	v.Observe("spip-001", "v1-1-gaaaaaaa")
	tick(time.Minute)
	v.Observe("spip-001", "v1-1-gaaaaaaa")
	s := v.Snapshot()
	if len(s) != 1 || s[0].Version != "v1-1-gaaaaaaa" || !s[0].VersionSince.Equal(t0) || !s[0].LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("same version again: %+v", s)
	}

	tick(time.Minute)
	v.Observe("spip-001", "v1-2-gbbbbbbb")
	s = v.Snapshot()
	if s[0].Version != "v1-2-gbbbbbbb" || !s[0].VersionSince.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("new version: %+v", s)
	}

	// A sensor that stops sending the header (an older build put back) is
	// recorded as reporting nothing, not left on the last version it sent.
	tick(time.Minute)
	v.Observe("spip-001", "")
	if s = v.Snapshot(); s[0].Version != "" || !s[0].VersionSince.Equal(t0.Add(3*time.Minute)) {
		t.Fatalf("header gone: %+v", s)
	}

	v.Observe("feint-001", "223cc9a")
	v.Observe("", "ignored")
	s = v.Snapshot()
	if len(s) != 2 || s[0].SensorID != "feint-001" || s[1].SensorID != "spip-001" {
		t.Fatalf("sorted snapshot: %+v", s)
	}
}

func TestSensorVersions_NilSafe(t *testing.T) {
	var v *SensorVersions
	v.Observe("spip-001", "x")
	v.Save()
	if v.Snapshot() != nil {
		t.Fatal("nil receiver returned a snapshot")
	}
}

func TestSensorVersions_PersistAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sensors.json")
	v := NewSensorVersions(path, nil, zerolog.Nop())
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	fixedClock(v, t0)
	v.Observe("spip-001", "v1-1-gaaaaaaa")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a version change did not write the state file: %v", err)
	}

	again := NewSensorVersions(path, nil, zerolog.Nop())
	tick := fixedClock(again, t0.Add(time.Hour))
	tick(0)
	again.Observe("spip-001", "v1-1-gaaaaaaa")
	s := again.Snapshot()
	if len(s) != 1 || !s[0].VersionSince.Equal(t0) {
		t.Fatalf("version_since lost over a restart: %+v", s)
	}
	if !s[0].LastSeen.Equal(t0.Add(time.Hour)) {
		t.Fatalf("last_seen not updated: %+v", s)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestSensorVersions_BadStateFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sensors.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := NewSensorVersions(path, nil, zerolog.Nop()).Snapshot(); len(s) != 0 {
		t.Fatalf("bad file gave %+v", s)
	}
	// A directory that cannot be written costs persistence, not ingest.
	v := NewSensorVersions(filepath.Join(dir, "missing", "sensors.json"), nil, zerolog.Nop())
	v.Observe("spip-001", "v1")
	if s := v.Snapshot(); len(s) != 1 {
		t.Fatalf("unwritable path dropped the record: %+v", s)
	}
}

func TestSensorVersions_MetricFollowsVersion(t *testing.T) {
	reg := prometheus.NewRegistry()
	v := NewSensorVersions("", reg, zerolog.Nop())
	v.Observe("spip-001", "v1")
	v.Observe("spip-001", "v2")
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var series []string
	for _, f := range families {
		if f.GetName() != "loom_sensor_version_info" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			series = append(series, labels["sensor_id"]+"="+labels["version"])
		}
	}
	if len(series) != 1 || series[0] != "spip-001=v2" {
		t.Fatalf("series %v, want only spip-001=v2 (the old label must go)", series)
	}
}

func TestHandler_RecordsVersionHeader(t *testing.T) {
	for _, tc := range []struct{ name, header, want string }{
		{"with header", "v1.4.0-12-g3d9d463", "v1.4.0-12-g3d9d463"},
		{"without header", "", ""},
		{"junk header", "not a version", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeTestHandler(t)
			h.Versions = NewSensorVersions("", nil, zerolog.Nop())
			body := mustJSON([]interface{}{spipStyleEvent("1.2.3.4", "sensor-a")})
			req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("X-Spip-ID", "sensor-a")
			if tc.header != "" {
				req.Header.Set(VersionHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204: the header must never decide acceptance", rec.Code)
			}
			s := h.Versions.Snapshot()
			if len(s) != 1 || s[0].SensorID != "sensor-a" || s[0].Version != tc.want {
				t.Fatalf("recorded %+v, want sensor-a at %q", s, tc.want)
			}
		})
	}
}

func TestHandler_UnauthenticatedVersionNotRecorded(t *testing.T) {
	h := makeTestHandler(t)
	h.Versions = NewSensorVersions("", nil, zerolog.Nop())
	req := httptest.NewRequest(http.MethodPost, "/ingest", bytes.NewReader([]byte("[]")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("X-Spip-ID", "sensor-a")
	req.Header.Set(VersionHeader, "v9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || len(h.Versions.Snapshot()) != 0 {
		t.Fatalf("status %d, recorded %+v", rec.Code, h.Versions.Snapshot())
	}
}
