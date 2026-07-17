// Package classify turns a captured first-payload into an application-protocol
// label using nDPI (ntop's deep-packet-inspection library). It replaces the
// hand-maintained byte/port rules that used to live in the ClickHouse
// materialized view: nDPI's ~300 dissectors are maintained upstream and ship
// with the distro package, so new protocol coverage arrives via `apt upgrade`,
// not by anyone editing a rule list.
//
// The real implementation is cgo against libndpi and is compiled only under the
// `ndpi` build tag (see classify_ndpi.go). Without that tag a no-op stub is
// used (classify_stub.go), so `go test ./...` and pure-Go dev builds keep
// working on machines that don't have libndpi installed.
package classify

// Classifier identifies the application protocol of a single captured payload.
// A nil *Classifier is valid and always returns "" — callers can treat "no
// classifier" and "unclassified" identically.
type Classifier interface {
	// Classify returns a lowercase protocol name (e.g. "bittorrent", "smbv1",
	// "postgresql") or "" when the payload can't be identified. srcPort/dstPort
	// are hints nDPI weighs alongside the payload signature.
	Classify(payload []byte, srcPort, dstPort uint16) string
}
