//go:build ndpi

package classify

/*
#cgo pkg-config: libndpi
#include <stdlib.h>
#include <string.h>
#include <ndpi/ndpi_api.h>
#include <ndpi/ndpi_typedefs.h>

// One detection module for the whole process. nDPI is not safe for concurrent
// ndpi_detection_process_packet() calls on the same module, so the Go side
// serialises access with a mutex; at our event rate that lock is free.
static struct ndpi_detection_module_struct *g_mod = NULL;

static int shim_init() {
    g_mod = ndpi_init_detection_module(ndpi_no_prefs);
    if (g_mod == NULL) return -1;
    NDPI_PROTOCOL_BITMASK all;
    NDPI_BITMASK_SET_ALL(all); // macro takes the struct and &'s it internally
    ndpi_set_protocol_detection_bitmask2(g_mod, &all);
    ndpi_finalize_initialization(g_mod);
    return 0;
}

// Synthesize a minimal IPv4+TCP L3 frame around the captured payload and run
// nDPI over it. We only ever have the attacker's first payload (one packet),
// so we process that single packet and immediately give up (with guessing) to
// let nDPI's port/first-bytes heuristics resolve protocols that would normally
// need more packets. Writes the resolved protocol name into out.
static void shim_classify(const unsigned char *payload, int plen,
                          unsigned short sport, unsigned short dport,
                          char *out, int out_sz) {
    out[0] = 0;
    if (g_mod == NULL || plen <= 0) return;

    int l3len = 40 + plen; // 20 IPv4 + 20 TCP + payload
    unsigned char *pkt = (unsigned char *)malloc(l3len);
    if (!pkt) return;
    memset(pkt, 0, 40);

    // IPv4 header (checksums are ignored by nDPI, left zero).
    pkt[0] = 0x45;                      // version 4, IHL 5
    pkt[2] = (unsigned char)((l3len >> 8) & 0xff);
    pkt[3] = (unsigned char)(l3len & 0xff);
    pkt[8] = 64;                        // TTL
    pkt[9] = 6;                         // protocol = TCP
    pkt[12] = 10; pkt[13] = 0; pkt[14] = 0; pkt[15] = 1; // src 10.0.0.1
    pkt[16] = 10; pkt[17] = 0; pkt[18] = 0; pkt[19] = 2; // dst 10.0.0.2

    // TCP header at offset 20.
    pkt[20] = (unsigned char)((sport >> 8) & 0xff); pkt[21] = (unsigned char)(sport & 0xff);
    pkt[22] = (unsigned char)((dport >> 8) & 0xff); pkt[23] = (unsigned char)(dport & 0xff);
    pkt[32] = 0x50;                     // data offset 5 (20 bytes)
    pkt[33] = 0x18;                     // flags PSH+ACK
    memcpy(pkt + 40, payload, plen);

    struct ndpi_flow_struct *flow =
        (struct ndpi_flow_struct *)ndpi_flow_malloc(SIZEOF_FLOW_STRUCT);
    if (!flow) { free(pkt); return; }
    memset(flow, 0, SIZEOF_FLOW_STRUCT);

    ndpi_protocol proto =
        ndpi_detection_process_packet(g_mod, flow, pkt, (unsigned short)l3len, 0);
    if (proto.app_protocol == NDPI_PROTOCOL_UNKNOWN &&
        proto.master_protocol == NDPI_PROTOCOL_UNKNOWN) {
        unsigned char guessed = 0;
        proto = ndpi_detection_giveup(g_mod, flow, 1, &guessed);
    }

    unsigned short id = proto.app_protocol;
    if (id == NDPI_PROTOCOL_UNKNOWN) id = proto.master_protocol;
    if (id != NDPI_PROTOCOL_UNKNOWN) {
        char *name = ndpi_get_proto_name(g_mod, id);
        if (name) { strncpy(out, name, out_sz - 1); out[out_sz - 1] = 0; }
    }

    ndpi_free_flow(flow); // frees nDPI-internal allocations AND the flow block
    free(pkt);
}
*/
import "C"

import (
	"errors"
	"strings"
	"sync"
	"unsafe"
)

type ndpiClassifier struct {
	mu sync.Mutex
}

// New initialises the shared nDPI detection module. Called once at startup.
func New() (Classifier, error) {
	if C.shim_init() != 0 {
		return nil, errors.New("ndpi: init_detection_module failed")
	}
	return &ndpiClassifier{}, nil
}

func (c *ndpiClassifier) Classify(payload []byte, srcPort, dstPort uint16) string {
	if len(payload) == 0 {
		return ""
	}
	var buf [64]C.char
	c.mu.Lock()
	C.shim_classify(
		(*C.uchar)(unsafe.Pointer(&payload[0])), C.int(len(payload)),
		C.ushort(srcPort), C.ushort(dstPort),
		&buf[0], C.int(len(buf)),
	)
	c.mu.Unlock()
	name := C.GoString(&buf[0])
	if name == "" || name == "Unknown" {
		return ""
	}
	return strings.ToLower(name)
}
