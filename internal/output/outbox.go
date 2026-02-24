package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type spoolFileMeta struct {
	name   string
	path   string
	size   int64
	events int
}

// diskOutbox is a simple NDJSON file spool for failed ClickHouse batches.
// Each file contains one batch (one ECS event map per line).
type diskOutbox struct {
	mu            sync.Mutex
	dir           string
	maxBytes      int64
	totalBytes    int64
	files         []spoolFileMeta
	seq           int64
	droppedEvents int64
}

func newDiskOutbox(dir string, maxBytes int64) (*diskOutbox, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	ob := &diskOutbox{
		dir:      dir,
		maxBytes: maxBytes,
		files:    make([]spoolFileMeta, 0),
	}
	if err := ob.reload(); err != nil {
		return nil, err
	}
	return ob, nil
}

func (o *diskOutbox) reload() error {
	ents, err := os.ReadDir(o.dir)
	if err != nil {
		return err
	}
	files := make([]spoolFileMeta, 0, len(ents))
	var total int64
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".ndjson") {
			continue
		}
		path := filepath.Join(o.dir, ent.Name())
		info, err := ent.Info()
		if err != nil {
			continue
		}
		events, err := countNDJSONLines(path)
		if err != nil {
			continue
		}
		files = append(files, spoolFileMeta{
			name:   ent.Name(),
			path:   path,
			size:   info.Size(),
			events: events,
		})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	o.files = files
	o.totalBytes = total
	return nil
}

func (o *diskOutbox) enqueue(batch []map[string]interface{}) (droppedEvents int, err error) {
	if len(batch) == 0 {
		return 0, nil
	}
	var body bytes.Buffer
	for _, ev := range batch {
		b, err := json.Marshal(ev)
		if err != nil {
			return 0, err
		}
		body.Write(b)
		body.WriteByte('\n')
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	name := fmt.Sprintf("%020d-%06d.ndjson", time.Now().UnixNano(), o.seq)
	tmp := filepath.Join(o.dir, name+".tmp")
	final := filepath.Join(o.dir, name)
	if err := os.WriteFile(tmp, body.Bytes(), 0o640); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	meta := spoolFileMeta{
		name:   name,
		path:   final,
		size:   int64(body.Len()),
		events: len(batch),
	}
	o.files = append(o.files, meta)
	sort.Slice(o.files, func(i, j int) bool { return o.files[i].name < o.files[j].name })
	o.totalBytes += meta.size
	droppedEvents = o.enforceMaxBytesLocked()
	return droppedEvents, nil
}

func (o *diskOutbox) enforceMaxBytesLocked() int {
	if o.maxBytes <= 0 {
		return 0
	}
	dropped := 0
	for o.totalBytes > o.maxBytes && len(o.files) > 1 {
		oldest := o.files[0]
		o.files = o.files[1:]
		o.totalBytes -= oldest.size
		o.droppedEvents += int64(oldest.events)
		dropped += oldest.events
		_ = os.Remove(oldest.path)
	}
	return dropped
}

func (o *diskOutbox) oldestMeta() (spoolFileMeta, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.files) == 0 {
		return spoolFileMeta{}, false
	}
	return o.files[0], true
}

func (o *diskOutbox) removeByName(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	idx := -1
	var meta spoolFileMeta
	for i, f := range o.files {
		if f.name == name {
			idx = i
			meta = f
			break
		}
	}
	if idx == -1 {
		return nil
	}
	o.files = append(o.files[:idx], o.files[idx+1:]...)
	o.totalBytes -= meta.size
	if o.totalBytes < 0 {
		o.totalBytes = 0
	}
	return os.Remove(meta.path)
}

func (o *diskOutbox) stats() (files int, bytes int64, droppedEvents int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.files), o.totalBytes, o.droppedEvents
}

func readBatchFile(path string) ([]map[string]interface{}, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := make([]map[string]interface{}, 0, 128)
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func countNDJSONLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)
	n := 0
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			n++
		}
	}
	return n, sc.Err()
}
