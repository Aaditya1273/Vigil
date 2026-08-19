// Package audit provides a tamper-evident record of every governance decision.
//
// Each event carries the SHA-256 hash of its own contents plus the hash of the
// event before it, so the file forms a chain. Editing, deleting, or reordering
// any record breaks the link at that point and every point after it, which is
// what Verify detects. This is not a distributed ledger and makes no claim
// beyond that: anyone who can rewrite the whole file can recompute the whole
// chain. It proves the file has not been *selectively* edited.
package audit

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Event is one governance decision.
type Event struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"ts"`
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	Tool      string    `json:"tool"`
	ArgsHash  string    `json:"args_hash"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	ModelUsed string    `json:"model_used"` // empty when no model was consulted
	Cost      float64   `json:"cost"`
	PrevHash  string    `json:"prev_hash"`
	Hash      string    `json:"hash"`
}

// fieldSep separates fields in the hash preimage. ASCII record separator: it
// cannot appear in any of the values we hash, so no value can impersonate a
// field boundary.
const fieldSep = "\x1e"

// computeHash derives an event's hash from an explicitly ordered preimage.
//
// Deliberately not sha256(json.Marshal(e)): Go's field order is stable only by
// convention, and adding a field later would silently change the hash of every
// historical event, invalidating chains that were never touched. Writing the
// fields out by hand makes the format an explicit decision.
func (e Event) computeHash() string {
	h := sha256.New()
	for _, f := range []string{
		e.ID,
		e.Timestamp.UTC().Format(time.RFC3339Nano),
		e.AgentID,
		e.SessionID,
		e.Tool,
		e.ArgsHash,
		e.Decision,
		e.Reason,
		e.ModelUsed,
		strconv.FormatFloat(e.Cost, 'f', -1, 64),
		e.PrevHash,
	} {
		h.Write([]byte(f))
		h.Write([]byte(fieldSep))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HashArgs fingerprints tool arguments.
//
// Arguments are hashed rather than stored: they routinely contain file paths,
// shell commands, and search patterns, and an audit log is exactly the wrong
// place to accumulate a copy of everything an agent ever touched. The hash
// still proves two calls were identical, which is what loop and replay
// detection need.
func HashArgs(args map[string]any) string {
	if len(args) == 0 {
		return hex.EncodeToString(sha256.New().Sum(nil))
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(fieldSep))
		// Marshal per value so a map value cannot forge a separator.
		v, err := json.Marshal(args[k])
		if err != nil {
			v = []byte(fmt.Sprintf("%v", args[k]))
		}
		h.Write(v)
		h.Write([]byte(fieldSep))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// newID returns a lexically sortable identifier: nanosecond timestamp plus four
// random bytes to break ties. Avoids a ULID dependency for a field whose only
// requirements are uniqueness and rough ordering.
func newID(t time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%d-%s", t.UTC().UnixNano(), hex.EncodeToString(b[:]))
}
