package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// DefaultPath is where the ledger lives when VIGIL_AUDIT_PATH is unset.
const DefaultPath = "vigil-audit.jsonl"

// Ledger is an append-only hash chain on disk, one JSON object per line.
//
// JSONL rather than a database: an append-only chain wants append-only storage,
// Verify can stream it a line at a time regardless of size, and the CLI can
// check a chain with no server and no driver.
type Ledger struct {
	mu   sync.Mutex
	f    *os.File
	last string // hash of the most recent event
	n    int
}

// Open opens or creates a ledger, recovering the chain head from any existing
// contents so appends continue the chain rather than starting a new one.
func Open(path string) (*Ledger, error) {
	if path == "" {
		path = DefaultPath
	}

	last, n, err := scanHead(path)
	if err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &Ledger{f: f, last: last, n: n}, nil
}

// scanHead reads an existing ledger to find the last hash and event count.
func scanHead(path string) (string, int, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	var last string
	var n int
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			// A malformed tail (e.g. a torn write) must not silently reset the
			// chain head to empty, which would let the next append look like a
			// fresh genesis record. Verify reports it; appending stops here.
			return last, n, nil
		}
		last = e.Hash
		n++
	}
	return last, n, sc.Err()
}

// Append seals an event into the chain and returns it with its assigned ID,
// timestamp, and hashes.
func (l *Ledger) Append(e Event) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	if e.ID == "" {
		e.ID = newID(e.Timestamp)
	}
	e.PrevHash = l.last
	e.Hash = e.computeHash()

	line, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return e, err
	}
	// fsync per event: an audit record that is still in the page cache when the
	// process dies did not happen. Cost is one syscall on a path that already
	// did a tool call.
	if err := l.f.Sync(); err != nil {
		return e, err
	}

	l.last = e.Hash
	l.n++
	return e, nil
}

// Len returns how many events are in the chain.
func (l *Ledger) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n
}

// Close flushes and closes the underlying file.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
