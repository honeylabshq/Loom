package enrich

import (
	"net"
	"sync"

	"github.com/oschwald/geoip2-golang"
	"github.com/rs/zerolog"
)

// Enricher adds ASN, GEO, and optionally DNS to ECS events.
type Enricher struct {
	geoDB   *geoip2.Reader
	asnDB   *geoip2.Reader
	dns     *DNSEnricher
	log     zerolog.Logger
	mu      sync.RWMutex
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
// Missing source.ip is non-fatal: enrichment is skipped and the event is preserved.
func (e *Enricher) EnrichEvent(event map[string]interface{}) {
	if event == nil {
		return
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
}

func setGeo(geo map[string]interface{}, city *geoip2.City) {
	if len(city.Country.IsoCode) == 2 {
		geo["country_iso_code"] = string(city.Country.IsoCode)
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
