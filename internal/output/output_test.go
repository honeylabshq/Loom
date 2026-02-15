package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func TestNewWriter_Stdout(t *testing.T) {
	w, err := NewWriter("stdout", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w == nil {
		t.Fatal("writer is nil")
	}
	ev := spipStyleEvent()
	if err := w.Write(ev); err != nil {
		t.Error(err)
	}
	if err := w.Close(); err != nil {
		t.Error(err)
	}
}

func TestNewWriter_UnknownType(t *testing.T) {
	_, err := NewWriter("unknown", "", "", "", "")
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestNewWriter_Elasticsearch_NoURL(t *testing.T) {
	_, err := NewWriter("elasticsearch", "", "", "", "")
	if err == nil {
		t.Fatal("expected error when elasticsearch_url is empty")
	}
}

func TestNewWriter_Elasticsearch_DefaultIndex(t *testing.T) {
	w, err := NewWriter("elasticsearch", "http://localhost:9200", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w == nil {
		t.Fatal("writer is nil")
	}
	_ = w.Close()
}

// spipStyleEvent returns a minimal ECS event as produced by Spip (roundtrip via JSON).
func spipStyleEvent() map[string]interface{} {
	return map[string]interface{}{
		"@timestamp": "2026-02-15T19:47:09Z",
		"event":      map[string]interface{}{"id": "abc", "ingested_by": "spip"},
		"source":     map[string]interface{}{"ip": "8.8.8.8", "port": float64(12345)},
	}
}

func TestSpipStyleEvent_JSONRoundtrip(t *testing.T) {
	ev := spipStyleEvent()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["@timestamp"] != "2026-02-15T19:47:09Z" {
		t.Error("roundtrip changed @timestamp")
	}
	src, _ := decoded["source"].(map[string]interface{})
	if src == nil || src["ip"] != "8.8.8.8" {
		t.Error("roundtrip changed source")
	}
}

func TestStdoutWriter_WriteToBuffer(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	w := &stdoutWriter{w: bufio.NewWriter(buf)}
	ev := spipStyleEvent()
	if err := w.Write(ev); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := buf.Bytes()
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("output not valid JSON: %s", out)
	}
	if decoded["@timestamp"] != "2026-02-15T19:47:09Z" {
		t.Errorf("output = %s", out)
	}
}
