package audit

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// maxLineBytes caps a single ledger record. Reasons are model-authored and
// could in principle be long; 1 MiB is far above any legitimate record and
// stops a corrupt file from being read into memory unbounded.
const maxLineBytes = 1 << 20

// Report is the outcome of verifying a chain.
type Report struct {
	OK bool `json:"ok"`
	// Count is the number of events considered: the whole chain, or just the
	// filtered session's events when one was requested.
	Count int `json:"count"`
	// FailedAt is the 1-indexed position in the *global* chain where
	// verification failed, or 0 when OK.
	FailedAt int    `json:"failed_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Verify walks the chain and reports the first break.
//
// sessionID filters which events are *counted*, not which are *checked*: the
// chain links every event regardless of session, so verifying a filtered subset
// would report a break at each session's first record. Integrity is always
// global; the filter only scopes the reported count.
func Verify(r io.Reader, sessionID string) (Report, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var prev string
	var pos, counted int

	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		pos++

		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			return Report{OK: false, Count: counted, FailedAt: pos, Reason: "malformed record"}, nil
		}

		// A record whose recomputed hash differs from the stored one has had
		// its contents edited.
		if e.computeHash() != e.Hash {
			return Report{OK: false, Count: counted, FailedAt: pos, Reason: "hash mismatch"}, nil
		}

		// A record whose prev_hash does not match the previous record's hash
		// means something was deleted, inserted, or reordered.
		if e.PrevHash != prev {
			return Report{OK: false, Count: counted, FailedAt: pos, Reason: "chain break"}, nil
		}

		prev = e.Hash
		if sessionID == "" || e.SessionID == sessionID {
			counted++
		}
	}
	if err := sc.Err(); err != nil {
		return Report{OK: false, Count: counted, FailedAt: pos}, err
	}

	return Report{OK: true, Count: counted}, nil
}

// VerifyFile verifies a ledger on disk. A file that does not exist is an empty,
// trivially valid chain — a deployment that has made no decisions yet has
// nothing to have tampered with.
func VerifyFile(path, sessionID string) (Report, error) {
	if path == "" {
		path = DefaultPath
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Report{OK: true, Count: 0}, nil
	}
	if err != nil {
		return Report{}, err
	}
	defer f.Close()
	return Verify(f, sessionID)
}

// Read returns events from a ledger, most recent last, optionally filtered by
// session and capped at limit (0 = all). Used by the decisions API.
func Read(path, sessionID string, limit int) ([]Event, error) {
	if path == "" {
		path = DefaultPath
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	events := make([]Event, 0, 64)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // Verify is what reports corruption; Read just skips it
		}
		if sessionID != "" && e.SessionID != sessionID {
			continue
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return events, err
	}

	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}
