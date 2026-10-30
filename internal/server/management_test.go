package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return resp.StatusCode, string(b)
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s: %v", url, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Extra routes are served on the management listener and nowhere else: the
// ingest listener is public, and /sensors names every sensor.
func TestManagementRoutesOnlyOnManagementListener(t *testing.T) {
	ingestAddr, mgmtAddr := freeAddr(t), freeAddr(t)
	s := &Server{
		IngestHandler:  http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		Logger:         zerolog.Nop(),
		ListenAddr:     ingestAddr,
		ManagementAddr: mgmtAddr,
		ManagementRoutes: map[string]http.Handler{
			"/version": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"commit":"abc1234"}`)) }),
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Run(ctx) }()

	code, body := get(t, "http://"+mgmtAddr+"/version")
	if code != http.StatusOK || !strings.Contains(body, "abc1234") {
		t.Fatalf("management /version: %d %q", code, body)
	}
	code, _ = get(t, "http://"+ingestAddr+"/version")
	if code == http.StatusOK {
		t.Fatal("/version answered on the ingest listener")
	}
}
