package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
)

// VersionHeader carries the sensor software's version on each batch request,
// for example "v1.4.0-12-g3d9d463" or "3d9d463". It is optional: a sensor
// built before the header existed sends none and is recorded with an empty
// version. The version lives in the request, never in the events, so the
// event schema stays as it is.
const VersionHeader = "X-Sensor-Version"

// A version is a git describe string or a commit; anything else is dropped
// rather than stored or used as a metric label.
var versionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// SensorState is what Loom knows about one sensor's software.
type SensorState struct {
	SensorID string `json:"sensor_id"`
	// Version is the last version the sensor reported, "" when it reports none.
	Version string `json:"version"`
	// VersionSince is the first batch Loom saw with this version: close to the
	// sensor's deploy time when Loom was running through it.
	VersionSince time.Time `json:"version_since"`
	LastSeen     time.Time `json:"last_seen"`
}

// SensorVersions records the version each authenticated sensor reports. With
// a state file it survives restarts, so VersionSince keeps meaning "since the
// sensor changed version" and not "since Loom last started". The file is
// written only when a version changes, which is rare, so the request path
// never waits on the disk otherwise.
type SensorVersions struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	sensors map[string]*SensorState
	path    string
	log     zerolog.Logger
	gauge   *prometheus.GaugeVec
	now     func() time.Time
}

// NewSensorVersions loads the state file when path is set and it exists. A
// missing or unreadable file starts empty; it is not a reason to refuse
// ingest. reg may be nil.
func NewSensorVersions(path string, reg prometheus.Registerer, log zerolog.Logger) *SensorVersions {
	v := &SensorVersions{
		sensors: map[string]*SensorState{},
		path:    path,
		log:     log,
		now:     func() time.Time { return time.Now().UTC() },
	}
	if reg != nil {
		v.gauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "loom_sensor_version_info",
			Help: "1 for the version each sensor last reported (empty when it reports none)",
		}, []string{"sensor_id", "version"})
		reg.MustRegister(v.gauge)
	}
	if path == "" {
		return v
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Str("path", path).Msg("sensor state file unreadable; starting empty")
		}
		return v
	}
	var saved struct {
		Sensors []SensorState `json:"sensors"`
	}
	if err := json.Unmarshal(b, &saved); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("sensor state file is not valid JSON; starting empty")
		return v
	}
	for i := range saved.Sensors {
		s := saved.Sensors[i]
		if s.SensorID == "" || (s.Version != "" && !versionRe.MatchString(s.Version)) {
			continue
		}
		v.sensors[s.SensorID] = &s
		if v.gauge != nil {
			v.gauge.WithLabelValues(s.SensorID, s.Version).Set(1)
		}
	}
	return v
}

// CleanVersion returns the header value when it is a plausible version, else "".
func CleanVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if versionRe.MatchString(raw) {
		return raw
	}
	return ""
}

// Observe records one authenticated request from sensorID with the raw
// header value. Safe on a nil receiver.
func (v *SensorVersions) Observe(sensorID, raw string) {
	if v == nil || sensorID == "" {
		return
	}
	version := CleanVersion(raw)
	now := v.now()
	v.mu.Lock()
	s, ok := v.sensors[sensorID]
	changed := !ok || s.Version != version
	var old string
	if !ok {
		s = &SensorState{SensorID: sensorID}
		v.sensors[sensorID] = s
	} else {
		old = s.Version
	}
	if changed {
		s.Version = version
		s.VersionSince = now
	}
	s.LastSeen = now
	var snap []SensorState
	if changed {
		snap = v.snapshotLocked()
	}
	v.mu.Unlock()

	if !changed {
		return
	}
	if v.gauge != nil {
		if ok {
			v.gauge.DeleteLabelValues(sensorID, old)
		}
		v.gauge.WithLabelValues(sensorID, version).Set(1)
	}
	if ok {
		v.log.Info().Str("sensor_id", sensorID).Str("from", old).Str("to", version).Msg("sensor version changed")
	} else {
		v.log.Info().Str("sensor_id", sensorID).Str("version", version).Msg("sensor version first seen")
	}
	v.save(snap)
}

// Snapshot returns every sensor seen, sorted by id.
func (v *SensorVersions) Snapshot() []SensorState {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.snapshotLocked()
}

// Save writes the current state, last-seen times included. Called at shutdown.
func (v *SensorVersions) Save() {
	if v == nil {
		return
	}
	v.save(v.Snapshot())
}

func (v *SensorVersions) snapshotLocked() []SensorState {
	out := make([]SensorState, 0, len(v.sensors))
	for _, s := range v.sensors {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SensorID < out[j].SensorID })
	return out
}

// save replaces the state file atomically: a temporary file in the same
// directory, then a rename, so a crash leaves the old file or the new one.
func (v *SensorVersions) save(snap []SensorState) {
	if v.path == "" {
		return
	}
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	b, err := json.MarshalIndent(struct {
		Sensors []SensorState `json:"sensors"`
	}{snap}, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(v.path), ".sensors-*.json")
	if err != nil {
		v.log.Warn().Err(err).Str("path", v.path).Msg("sensor state not saved")
		return
	}
	name := tmp.Name()
	_, werr := tmp.Write(append(b, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		v.log.Warn().Str("path", v.path).Msg("sensor state not saved: write failed")
		return
	}
	if err := os.Rename(name, v.path); err != nil {
		_ = os.Remove(name)
		v.log.Warn().Err(err).Str("path", v.path).Msg("sensor state not saved")
	}
}
