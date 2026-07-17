package enrich

import (
	"encoding/hex"
	"net"
	"sync"
	"time"

	"github.com/StefanGrimminck/Loom/internal/classify"
	"github.com/oschwald/geoip2-golang"
	"github.com/rs/zerolog"
)

// Enricher adds ASN, GEO, optionally DNS, and optionally an application-protocol
// label (nDPI) to ECS events.
type Enricher struct {
	geoDB      *geoip2.Reader
	asnDB      *geoip2.Reader
	dns        *DNSEnricher
	classifier classify.Classifier
	log        zerolog.Logger
	mu         sync.RWMutex
}

// SetClassifier attaches an application-protocol classifier. Safe to leave
// unset (nil): classification is then skipped and the event is unchanged.
func (e *Enricher) SetClassifier(c classify.Classifier) {
	e.classifier = c
}

// NewEnricher opens MaxMind DBs and optional DNS enricher. geoPath and asnPath can be "" to skip.
func NewEnricher(geoPath, asnPath string, dns *DNSEnricher, log zerolog.Logger) (*Enricher, error) {
	e := &Enricher{log: log, dns: dns}
	if geoPath != "" {
		db, err := geoip2.Open(geoPath)
		if err != nil {
			return nil, err
		}
		e.geoDB = db
	}
	if asnPath != "" {
		db, err := geoip2.Open(asnPath)
		if err != nil {
			if e.geoDB != nil {
				_ = e.geoDB.Close()
			}
			return nil, err
		}
		e.asnDB = db
	}
	return e, nil
}

// Close closes DBs.
func (e *Enricher) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.geoDB != nil {
		_ = e.geoDB.Close()
		e.geoDB = nil
	}
	if e.asnDB != nil {
		_ = e.asnDB.Close()
		e.asnDB = nil
	}
	return nil
}

// EnrichEvent enriches one ECS-like map. Preserves all existing keys; adds source.as.*, source.geo.*, source.domain.
// Injects @timestamp (RFC3339Nano, UTC) if not already present.
// Missing source.ip is non-fatal: enrichment is skipped and the event is preserved.
func (e *Enricher) EnrichEvent(event map[string]interface{}) {
	if event == nil {
		return
	}
	if _, ok := event["@timestamp"]; !ok {
		event["@timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	source, _ := event["source"].(map[string]interface{})
	if source == nil {
		source = make(map[string]interface{})
		event["source"] = source
	}
	ipStr, _ := source["ip"].(string)
	if ipStr == "" {
		return
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return
	}

	// ASN
	if e.asnDB != nil {
		e.mu.RLock()
		asn, err := e.asnDB.ASN(ip)
		e.mu.RUnlock()
		if err == nil && asn != nil {
			if as, ok := source["as"].(map[string]interface{}); ok && as != nil {
				as["number"] = int(asn.AutonomousSystemNumber)
				if asn.AutonomousSystemOrganization != "" {
					if asOrg, ok := as["organization"].(map[string]interface{}); ok && asOrg != nil {
						asOrg["name"] = asn.AutonomousSystemOrganization
					} else {
						as["organization"] = map[string]interface{}{"name": asn.AutonomousSystemOrganization}
					}
				}
			} else {
				as := map[string]interface{}{"number": int(asn.AutonomousSystemNumber)}
				if asn.AutonomousSystemOrganization != "" {
					as["organization"] = map[string]interface{}{"name": asn.AutonomousSystemOrganization}
				}
				source["as"] = as
			}
		}
	}

	// GEO (City DB)
	if e.geoDB != nil {
		e.mu.RLock()
		city, err := e.geoDB.City(ip)
		e.mu.RUnlock()
		if err == nil && city != nil {
			if geo, ok := source["geo"].(map[string]interface{}); ok && geo != nil {
				setGeo(geo, city)
			} else {
				geo := make(map[string]interface{})
				setGeo(geo, city)
				source["geo"] = geo
			}
		}
	}

	// DNS PTR
	if e.dns != nil {
		if name := e.dns.LookupPTR(ip); name != "" {
			source["domain"] = name
		}
	}

	// Application-protocol classification (nDPI). Fed the captured first
	// payload (event.original_payload_hex, the clean hex copy, not the
	// UTF-8-mangled event.summary) plus the ports. The result goes under a
	// dedicated `ndpi.protocol` key so it never collides with the existing
	// network.protocol semantics; ClickHouse prefers it and falls back to
	// its own classifier when it's absent.
	if e.classifier != nil {
		if proto := e.classifyEvent(event, source); proto != "" {
			ndpi, _ := event["ndpi"].(map[string]interface{})
			if ndpi == nil {
				ndpi = make(map[string]interface{})
				event["ndpi"] = ndpi
			}
			ndpi["protocol"] = proto
		}
	}
}

// classifyEvent pulls the payload hex + ports out of an ECS event and runs the
// classifier. Returns "" when there's nothing to classify or no match.
func (e *Enricher) classifyEvent(event, source map[string]interface{}) string {
	ev, _ := event["event"].(map[string]interface{})
	if ev == nil {
		return ""
	}
	hexStr, _ := ev["original_payload_hex"].(string)
	if hexStr == "" {
		return ""
	}
	payload, err := hex.DecodeString(hexStr)
	if err != nil || len(payload) == 0 {
		return ""
	}
	srcPort := portOf(source["port"])
	var dstPort uint16
	if dst, ok := event["destination"].(map[string]interface{}); ok {
		dstPort = portOf(dst["port"])
	}
	return e.classifier.Classify(payload, srcPort, dstPort)
}

// portOf coerces a JSON-decoded port (float64, int, or json.Number) to uint16.
func portOf(v interface{}) uint16 {
	switch n := v.(type) {
	case float64:
		return uint16(n)
	case int:
		return uint16(n)
	case int64:
		return uint16(n)
	}
	return 0
}

func setGeo(geo map[string]interface{}, city *geoip2.City) {
	if len(city.Country.IsoCode) == 2 {
		geo["country_iso_code"] = string(city.Country.IsoCode)
	}
	if name, ok := city.Country.Names["en"]; ok && name != "" {
		geo["country_name"] = name
	}
	if city.Subdivisions != nil && len(city.Subdivisions) > 0 {
		geo["region_name"] = city.Subdivisions[0].Names["en"]
	}
	if city.City.Names != nil {
		if name, ok := city.City.Names["en"]; ok {
			geo["city_name"] = name
		}
	}
	if city.Location.Latitude != 0 || city.Location.Longitude != 0 {
		geo["location"] = map[string]interface{}{
			"lat": city.Location.Latitude,
			"lon": city.Location.Longitude,
		}
	}
}

// Ready returns true when the enricher can be used (always true; no DBs means pass-through).
func (e *Enricher) Ready() bool {
	return true
}
