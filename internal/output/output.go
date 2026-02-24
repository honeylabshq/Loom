package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Writer emits one enriched ECS document per event to a configured destination.
type Writer interface {
	Write(event map[string]interface{}) error
	Flush() error
	Close() error
}

// FlushLogger is called after each ClickHouse flush (rows written, or err if failed).
// Used for logging; may be nil.
type FlushLogger func(rows int, err error)

// OutboxConfig controls local disk spooling for failed ClickHouse writes.
type OutboxConfig struct {
	Enabled         bool
	Dir             string
	MaxBytes        int64
	MaxBatchSize    int
	RetryBackoff    time.Duration
	RetryMaxBackoff time.Duration
}

// WriterConfig holds all output backend options; only fields for the chosen type are used.
type WriterConfig struct {
	Type               string
	ElasticsearchURL   string
	ElasticsearchIndex string
	ElasticsearchUser  string
	ElasticsearchPass  string
	ClickHouseURL      string
	ClickHouseDatabase string
	ClickHouseTable    string
	ClickHouseUser     string
	ClickHousePassword string
	ClickHouseFlushLog FlushLogger // optional: log each flush (success or failure)
	ClickHouseOutbox   OutboxConfig
	SkipClickHousePing bool // if true, skip startup connection check (for tests)
}

// NewWriter creates a Writer from config. Type: "stdout", "elasticsearch", "clickhouse".
func NewWriter(cfg WriterConfig) (Writer, error) {
	switch cfg.Type {
	case "stdout":
		return &stdoutWriter{w: bufio.NewWriter(os.Stdout)}, nil
	case "elasticsearch":
		if cfg.ElasticsearchURL == "" {
			return nil, fmt.Errorf("elasticsearch_url required")
		}
		idx := cfg.ElasticsearchIndex
		if idx == "" {
			idx = "loom-events"
		}
		client := &http.Client{Timeout: 30 * time.Second}
		return &esWriter{
			client: client,
			url:    strings.TrimSuffix(cfg.ElasticsearchURL, "/") + "/_bulk",
			index:  idx,
			user:   cfg.ElasticsearchUser,
			pass:   cfg.ElasticsearchPass,
			buf:    make([]map[string]interface{}, 0, 100),
			flush:  100,
		}, nil
	case "clickhouse":
		if cfg.ClickHouseURL == "" {
			return nil, fmt.Errorf("clickhouse_url required")
		}
		db := cfg.ClickHouseDatabase
		if db == "" {
			db = "default"
		}
		tbl := cfg.ClickHouseTable
		if tbl == "" {
			tbl = "loom_events"
		}
		client := &http.Client{Timeout: 30 * time.Second}
		if !cfg.SkipClickHousePing {
			if err := pingClickHouse(client, cfg.ClickHouseURL, cfg.ClickHouseUser, cfg.ClickHousePassword); err != nil {
				return nil, fmt.Errorf("clickhouse connection check failed: %w", err)
			}
		}
		return newClickHouseWriter(
			client,
			cfg.ClickHouseURL,
			db,
			tbl,
			cfg.ClickHouseUser,
			cfg.ClickHousePassword,
			cfg.ClickHouseFlushLog,
			cfg.ClickHouseOutbox,
		)
	default:
		return nil, fmt.Errorf("unknown output type: %s", cfg.Type)
	}
}

type stdoutWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (s *stdoutWriter) Write(event map[string]interface{}) error {
	if event == nil {
		return nil
	}
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

func (s *stdoutWriter) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Flush()
}

type esWriter struct {
	client *http.Client
	url    string
	index  string
	user   string
	pass   string
	mu     sync.Mutex
	buf    []map[string]interface{}
	flush  int
}

func (e *esWriter) Write(event map[string]interface{}) error {
	if event == nil {
		return nil
	}
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

func (e *esWriter) Flush() error {
	return e.flushBuf()
}

func (e *esWriter) Close() error {
	return e.flushBuf()
}

// pingClickHouse runs SELECT 1 against the server to verify connectivity and auth.
func pingClickHouse(client *http.Client, baseURL, user, pass string) error {
	url := strings.TrimSuffix(baseURL, "/") + "/?query=" + url.QueryEscape("SELECT 1")
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if user != "" || pass != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ping %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// clickHouseWriter sends enriched events to ClickHouse via HTTP INSERT with JSONEachRow.
// Table must have at least: event String (full ECS JSON). See docs for schema.
type clickHouseWriter struct {
	client   *http.Client
	url      string
	db       string
	table    string
	user     string
	pass     string
	flushLog FlushLogger
	outbox   *diskOutbox

	mu              sync.Mutex
	buf             []map[string]interface{}
	flush           int
	retryBackoff    time.Duration
	retryMax        time.Duration
	nextRetryAt     time.Time
	currentBackoff  time.Duration
	outboxBatchSize int
}

func newClickHouseWriter(
	client *http.Client,
	baseURL,
	database,
	table,
	user,
	pass string,
	flushLog FlushLogger,
	outboxCfg OutboxConfig,
) (*clickHouseWriter, error) {
	w := &clickHouseWriter{
		client:          client,
		url:             strings.TrimSuffix(baseURL, "/"),
		db:              database,
		table:           table,
		user:            user,
		pass:            pass,
		flushLog:        flushLog,
		buf:             make([]map[string]interface{}, 0, 100),
		flush:           100,
		retryBackoff:    outboxCfg.RetryBackoff,
		retryMax:        outboxCfg.RetryMaxBackoff,
		currentBackoff:  outboxCfg.RetryBackoff,
		outboxBatchSize: outboxCfg.MaxBatchSize,
	}
	if w.retryBackoff <= 0 {
		w.retryBackoff = time.Second
		w.currentBackoff = time.Second
	}
	if w.retryMax <= 0 {
		w.retryMax = 30 * time.Second
	}
	if w.outboxBatchSize <= 0 {
		w.outboxBatchSize = w.flush
	}
	if outboxCfg.Enabled {
		ob, err := newDiskOutbox(outboxCfg.Dir, outboxCfg.MaxBytes)
		if err != nil {
			return nil, err
		}
		w.outbox = ob
	}
	return w, nil
}

func (c *clickHouseWriter) Write(event map[string]interface{}) error {
	if event == nil {
		return nil
	}
	c.mu.Lock()
	c.buf = append(c.buf, event)
	shouldFlush := len(c.buf) >= c.flush
	c.mu.Unlock()
	if shouldFlush {
		return c.Flush()
	}
	return nil
}

func (c *clickHouseWriter) Flush() error {
	if err := c.flushBuf(); err != nil {
		return err
	}
	return c.drainOutbox()
}

func (c *clickHouseWriter) flushBuf() error {
	c.mu.Lock()
	if len(c.buf) == 0 {
		c.mu.Unlock()
		return nil
	}
	batch := c.buf
	c.buf = make([]map[string]interface{}, 0, c.flush)
	c.mu.Unlock()
	if err := c.insertBatch(batch); err != nil {
		if c.outbox != nil {
			dropped := 0
			for _, chunk := range splitBatches(batch, c.outboxBatchSize) {
				d, qerr := c.outbox.enqueue(chunk)
				dropped += d
				if qerr != nil {
					if c.flushLog != nil {
						c.flushLog(len(batch), fmt.Errorf("clickhouse insert failed and outbox enqueue failed: %w (insert err: %v)", qerr, err))
					}
					return qerr
				}
			}
			if c.flushLog != nil {
				files, bytes, _ := c.outbox.stats()
				c.flushLog(
					len(batch),
					fmt.Errorf("clickhouse insert failed; queued to outbox (dropped_oldest_events=%d queue_files=%d queue_bytes=%d): %w", dropped, files, bytes, err),
				)
			}
			return nil
		}
		if c.flushLog != nil {
			c.flushLog(len(batch), err)
		}
		return err
	}
	if c.flushLog != nil {
		c.flushLog(len(batch), nil)
	}
	return nil
}

func (c *clickHouseWriter) insertBatch(batch []map[string]interface{}) error {
	var body bytes.Buffer
	for _, ev := range batch {
		eventJSON, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		row := map[string]string{"event": string(eventJSON)}
		rowJSON, _ := json.Marshal(row)
		body.Write(rowJSON)
		body.WriteByte('\n')
	}
	query := fmt.Sprintf("INSERT INTO %s.%s (event) FORMAT JSONEachRow", c.db, c.table)
	reqURL := c.url + "/?query=" + url.QueryEscape(query)
	req, err := http.NewRequest(http.MethodPost, reqURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("clickhouse insert %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *clickHouseWriter) drainOutbox() error {
	if c.outbox == nil {
		return nil
	}
	if !c.nextRetryAt.IsZero() && time.Now().Before(c.nextRetryAt) {
		return nil
	}
	for i := 0; i < 10; i++ {
		meta, ok := c.outbox.oldestMeta()
		if !ok {
			c.currentBackoff = c.retryBackoff
			c.nextRetryAt = time.Time{}
			return nil
		}
		batch, err := readBatchFile(meta.path)
		if err != nil {
			_ = c.outbox.removeByName(meta.name)
			if c.flushLog != nil {
				c.flushLog(meta.events, fmt.Errorf("outbox file unreadable, dropped batch %q: %w", meta.name, err))
			}
			continue
		}
		if err := c.insertBatch(batch); err != nil {
			if c.flushLog != nil {
				c.flushLog(len(batch), fmt.Errorf("outbox drain failed: %w", err))
			}
			c.nextRetryAt = time.Now().Add(c.currentBackoff)
			c.currentBackoff *= 2
			if c.currentBackoff > c.retryMax {
				c.currentBackoff = c.retryMax
			}
			return nil
		}
		if err := c.outbox.removeByName(meta.name); err != nil && c.flushLog != nil {
			c.flushLog(len(batch), fmt.Errorf("outbox drain delete failed: %w", err))
		}
		if c.flushLog != nil {
			c.flushLog(len(batch), nil)
		}
	}
	return nil
}

func splitBatches(batch []map[string]interface{}, size int) [][]map[string]interface{} {
	if size <= 0 || len(batch) <= size {
		return [][]map[string]interface{}{batch}
	}
	out := make([][]map[string]interface{}, 0, (len(batch)+size-1)/size)
	for i := 0; i < len(batch); i += size {
		j := i + size
		if j > len(batch) {
			j = len(batch)
		}
		out = append(out, batch[i:j])
	}
	return out
}

func (c *clickHouseWriter) Close() error {
	if err := c.flushBuf(); err != nil {
		return err
	}
	return c.drainOutbox()
}
