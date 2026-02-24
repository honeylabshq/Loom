package output

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClickHouseOutbox_QueueAndDrain(t *testing.T) {
	var failInserts atomic.Bool
	failInserts.Store(true)
	var insertedRows atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "SELECT+1") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("1"))
			return
		}
		if failInserts.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		sc := bufio.NewScanner(strings.NewReader(string(body)))
		count := int64(0)
		for sc.Scan() {
			if strings.TrimSpace(sc.Text()) != "" {
				count++
			}
		}
		insertedRows.Add(count)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	w, err := NewWriter(WriterConfig{
		Type:               "clickhouse",
		ClickHouseURL:      srv.URL,
		ClickHouseDatabase: "default",
		ClickHouseTable:    "loom_events",
		ClickHouseOutbox: OutboxConfig{
			Enabled:         true,
			Dir:             outDir,
			MaxBytes:        10 * 1024 * 1024,
			MaxBatchSize:    100,
			RetryBackoff:    10 * time.Millisecond,
			RetryMaxBackoff: 50 * time.Millisecond,
		},
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	defer func() { _ = w.Close() }()

	for i := 0; i < 7; i++ {
		if err := w.Write(spipStyleEvent()); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush with failed ClickHouse should not be fatal when outbox enabled: %v", err)
	}
	if insertedRows.Load() != 0 {
		t.Fatalf("expected zero inserted rows while clickhouse failing, got %d", insertedRows.Load())
	}
	if n := countSpoolFiles(t, outDir); n == 0 {
		t.Fatal("expected outbox spool files after failed insert")
	}

	failInserts.Store(false)
	time.Sleep(20 * time.Millisecond)
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush after recovery: %v", err)
	}
	if insertedRows.Load() == 0 {
		t.Fatal("expected drained outbox rows after clickhouse recovery")
	}
	if n := countSpoolFiles(t, outDir); n != 0 {
		t.Fatalf("expected outbox fully drained, files left: %d", n)
	}
}

func TestDiskOutbox_DropOldestOnOverflow(t *testing.T) {
	dir := t.TempDir()
	ob, err := newDiskOutbox(dir, 500)
	if err != nil {
		t.Fatal(err)
	}
	large := map[string]interface{}{
		"event": map[string]interface{}{
			"id":      "x",
			"summary": strings.Repeat("A", 400),
		},
	}
	if dropped, err := ob.enqueue([]map[string]interface{}{large}); err != nil {
		t.Fatal(err)
	} else if dropped != 0 {
		t.Fatalf("unexpected initial dropped count: %d", dropped)
	}
	if dropped, err := ob.enqueue([]map[string]interface{}{large}); err != nil {
		t.Fatal(err)
	} else if dropped == 0 {
		t.Fatal("expected dropping oldest events when queue overflows")
	}
	files, _, droppedTotal := ob.stats()
	if files == 0 {
		t.Fatal("expected at least one file to remain after overflow handling")
	}
	if droppedTotal == 0 {
		t.Fatal("expected droppedEvents metric to increment")
	}
}

func countSpoolFiles(t *testing.T, dir string) int {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(filepath.Base(e.Name()), ".ndjson") {
			n++
		}
	}
	return n
}
