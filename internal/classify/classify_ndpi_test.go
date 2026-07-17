//go:build ndpi

package classify

import (
	"encoding/hex"
	"strings"
	"testing"
)

// Real first-payloads captured by the honeypots (from honeylabs.ecs_logs), each
// a protocol the old ClickHouse rules left unclassified. Verifies nDPI labels
// them and that the label is stable, so a libndpi upgrade that changes a name
// is caught here rather than silently in production.
func TestClassifyKnownPayloads(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		name    string
		dstPort uint16
		hexPay  string
		want    string // substring expected in the lowercased label; "" = must stay blank
	}{
		{"bittorrent", 39131, "13426974546f7272656e742070726f746f636f6c00000000001800051bba521fdbc6b84ea3119a59eb71177f7c4fbc3c2d5557313630392d4d494344", "bittorrent"},
		{"mssql", 1433, "1201003400000000000015000601001b000102001c000c0300280004ff080001550000004d5353514c53657276657200bc0a0000", "mssql"},
		{"rtsp", 8554, "444553435249424520727473703a2f2f352e3137352e3138332e3133323a383535342f53747265616d696e672f4368616e6e656c732f31303120525453502f312e300d0a435365713a2034370d0a557365722d4167656e743a204c61766636302e332e31", "rtsp"},
		// SMB is deliberately blank: nDPI 4.2 only resolves this negotiate
		// packet by PORT (Match by port), which we reject as a guess. The old
		// rules didn't catch it either. Honest blank beats a port guess.
		{"smb_port_only", 445, "00000045ff534d4272000000001801c8000000000000000000000000ffff000000000000002200024e54204c4d20302e31320002534d4220322e3030", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pay, err := hex.DecodeString(tc.hexPay)
			if err != nil {
				t.Fatalf("hex: %v", err)
			}
			got := c.Classify(pay, 40000, tc.dstPort)
			if tc.want == "" {
				if got != "" {
					t.Errorf("Classify() = %q, want blank (port-only guess must be rejected)", got)
				}
			} else if !strings.Contains(got, tc.want) {
				t.Errorf("Classify() = %q, want substring %q", got, tc.want)
			}
		})
	}
}

// Guard against the flow-cache contamination bug: classifying one protocol must
// not bleed its label onto the next unrelated payload.
func TestClassifyNoContamination(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bt, _ := hex.DecodeString("13426974546f7272656e742070726f746f636f6c00000000001800051bba521fdbc6b84ea3119a59eb71177f7c4fbc3c2d5557313630392d4d494344")
	rtsp, _ := hex.DecodeString("444553435249424520727473703a2f2f352e3137352e3138332e3133323a383535342f53747265616d696e672f4368616e6e656c732f31303120525453502f312e30")
	// Alternate them; each must classify as itself, never inherit the other.
	for i := 0; i < 200; i++ {
		if got := c.Classify(bt, 40000, 39131); got != "" && !strings.Contains(got, "bittorrent") {
			t.Fatalf("bittorrent bled to %q at i=%d", got, i)
		}
		if got := c.Classify(rtsp, 40000, 8554); strings.Contains(got, "bittorrent") {
			t.Fatalf("rtsp returned bittorrent (contamination) at i=%d", i)
		}
	}
}

// Empty and junk payloads must never crash and must classify as "".
func TestClassifyEdgeCases(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := c.Classify(nil, 1, 2); got != "" {
		t.Errorf("nil payload = %q, want empty", got)
	}
	if got := c.Classify([]byte{0x00}, 1, 2); got != "" {
		t.Errorf("1-byte junk = %q, want empty", got)
	}
}

// Hammer the classifier to shake out leaks / double-frees in the per-call
// flow malloc/free path. Run with -race for concurrency safety.
func TestClassifyStress(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	pay, _ := hex.DecodeString("13426974546f7272656e742070726f746f636f6c")
	for i := 0; i < 20000; i++ {
		_ = c.Classify(pay, 40000, 39131)
	}
}
