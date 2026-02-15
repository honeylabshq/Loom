package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Writer emits one enriched ECS document per event to a configured destination.
type Writer interface {
	Write(event map[string]interface{}) error
	Close() error
}

// NewWriter creates a Writer from type and config. type: "stdout", "elasticsearch".
func NewWriter(typ, esURL, esIndex, esUser, esPass string) (Writer, error) {
	switch typ {
	case "stdout":
		return &stdoutWriter{w: bufio.NewWriter(os.Stdout)}, nil
	case "elasticsearch":
		if esURL == "" {
			return nil, fmt.Errorf("elasticsearch_url required")
		}
		idx := esIndex
		if idx == "" {
			idx = "loom-events"
		}
		client := &http.Client{Timeout: 30 * time.Second}
		return &esWriter{
			client: client,
			url:    strings.TrimSuffix(esURL, "/") + "/_bulk",
			index:  idx,
			user:   esUser,
			pass:   esPass,
			buf:    make([]map[string]interface{}, 0, 100),
			flush:  100,
		}, nil
	default:
		return nil, fmt.Errorf("unknown output type: %s", typ)
	}
}

type stdoutWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (s *stdoutWriter) Write(event map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := s.w.Write(append(b, '\n')); err != nil {
		return err
	}
	return s.w.Flush()
}

func (s *stdoutWriter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Flush()
}

type esWriter struct {
	client   *http.Client
	url      string
	index    string
	user   string
	pass   string
	mu     sync.Mutex
	buf    []map[string]interface{}
	flush  int
}

func (e *esWriter) Write(event map[string]interface{}) error {
	e.mu.Lock()
	e.buf = append(e.buf, event)
	shouldFlush := len(e.buf) >= e.flush
	e.mu.Unlock()
	if shouldFlush {
		return e.flushBuf()
	}
	return nil
}

func (e *esWriter) flushBuf() error {
	e.mu.Lock()
	if len(e.buf) == 0 {
		e.mu.Unlock()
		return nil
	}
	batch := e.buf
	e.buf = make([]map[string]interface{}, 0, e.flush)
	e.mu.Unlock()

	var ndjson bytes.Buffer
	for _, ev := range batch {
		// Bulk action: index to index
		meta := map[string]interface{}{"index": map[string]interface{}{"_index": e.index}}
		metaB, _ := json.Marshal(meta)
		ndjson.Write(metaB)
		ndjson.WriteByte('\n')
		docB, _ := json.Marshal(ev)
		ndjson.Write(docB)
		ndjson.WriteByte('\n')
	}
	req, err := http.NewRequest(http.MethodPost, e.url, &ndjson)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	if e.user != "" && e.pass != "" {
		req.SetBasicAuth(e.user, e.pass)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("elasticsearch bulk %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (e *esWriter) Close() error {
	return e.flushBuf()
}
