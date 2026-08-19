package audit_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
)

// writeChain appends n events and returns the ledger path.
func writeChain(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	for i := 0; i < n; i++ {
		_, err := l.Append(audit.Event{
			AgentID:   "agent-a",
			SessionID: "sess-1",
			Tool:      "read_file",
			ArgsHash:  audit.HashArgs(map[string]any{"path": "x.go"}),
			Decision:  "ALLOW",
			Reason:    "permitted by declared intent",
			Cost:      0.001,
		})
		if err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	return path
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func writeLines(t *testing.T, path string, ls []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(ls, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestValidChain(t *testing.T) {
	path := writeChain(t, 10)

	rep, err := audit.VerifyFile(path, "")
	if err != nil {
		t.Fatalf("VerifyFile failed: %v", err)
	}
	if !rep.OK {
		t.Fatalf("Expected a valid chain, got failure at %d: %s", rep.FailedAt, rep.Reason)
	}
	if rep.Count != 10 {
		t.Fatalf("Expected 10 events verified, got %d", rep.Count)
	}
}

func TestTamperedEvent(t *testing.T) {
	path := writeChain(t, 8)
	ls := lines(t, path)

	// Rewrite a decision in the middle: the classic "make a block look like an
	// allow" edit.
	ls[3] = strings.Replace(ls[3], `"decision":"ALLOW"`, `"decision":"BLOCK"`, 1)
	writeLines(t, path, ls)

	rep, _ := audit.VerifyFile(path, "")
	if rep.OK {
		t.Fatal("Expected tampering to be detected")
	}
	if rep.FailedAt != 4 {
		t.Fatalf("Expected failure at event 4, got %d", rep.FailedAt)
	}
	if rep.Reason != "hash mismatch" {
		t.Fatalf("Expected 'hash mismatch', got %q", rep.Reason)
	}
}

func TestMissingEvent(t *testing.T) {
	path := writeChain(t, 8)
	ls := lines(t, path)

	// Delete a middle record: the surviving successor's prev_hash no longer
	// matches its new predecessor.
	writeLines(t, path, append(append([]string{}, ls[:4]...), ls[5:]...))

	rep, _ := audit.VerifyFile(path, "")
	if rep.OK {
		t.Fatal("Expected a deleted event to be detected")
	}
	if rep.Reason != "chain break" {
		t.Fatalf("Expected 'chain break', got %q", rep.Reason)
	}
	if rep.FailedAt != 5 {
		t.Fatalf("Expected failure at position 5, got %d", rep.FailedAt)
	}
}

func TestReorderedEvents(t *testing.T) {
	path := writeChain(t, 8)
	ls := lines(t, path)

	ls[2], ls[5] = ls[5], ls[2]
	writeLines(t, path, ls)

	rep, _ := audit.VerifyFile(path, "")
	if rep.OK {
		t.Fatal("Expected reordering to be detected")
	}
	if rep.Reason != "chain break" {
		t.Fatalf("Expected 'chain break', got %q", rep.Reason)
	}
}

func TestMalformedRecord(t *testing.T) {
	path := writeChain(t, 4)
	ls := lines(t, path)
	ls[2] = "{not json"
	writeLines(t, path, ls)

	rep, _ := audit.VerifyFile(path, "")
	if rep.OK {
		t.Fatal("Expected a malformed record to be detected")
	}
	if rep.Reason != "malformed record" {
		t.Fatalf("Expected 'malformed record', got %q", rep.Reason)
	}
}

func TestEmptyAndMissingLedgerAreValid(t *testing.T) {
	rep, err := audit.VerifyFile(filepath.Join(t.TempDir(), "nope.jsonl"), "")
	if err != nil {
		t.Fatalf("VerifyFile on a missing file failed: %v", err)
	}
	if !rep.OK || rep.Count != 0 {
		t.Fatalf("Expected a missing ledger to verify as an empty chain, got %+v", rep)
	}
}

func TestGenesisHasEmptyPrevHash(t *testing.T) {
	path := writeChain(t, 3)
	events, err := audit.Read(path, "", 0)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if events[0].PrevHash != "" {
		t.Fatalf("Expected the first event to have an empty prev_hash, got %q", events[0].PrevHash)
	}
	for i := 1; i < len(events); i++ {
		if events[i].PrevHash != events[i-1].Hash {
			t.Fatalf("Event %d prev_hash does not link to its predecessor", i)
		}
	}
}

// TestIdenticalPayloadsGetDistinctHashes: two identical decisions must still be
// distinguishable, otherwise a duplicate could be silently dropped from the
// chain without breaking it.
func TestIdenticalPayloadsGetDistinctHashes(t *testing.T) {
	path := writeChain(t, 2)
	events, _ := audit.Read(path, "", 0)
	if events[0].Hash == events[1].Hash {
		t.Fatal("Expected identical payloads to hash differently (id and prev_hash differ)")
	}
}

// TestAppendResumesExistingChain: reopening a ledger must continue the chain,
// not start a second genesis record.
func TestAppendResumesExistingChain(t *testing.T) {
	path := writeChain(t, 3)

	l, err := audit.Open(path)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	if _, err := l.Append(audit.Event{SessionID: "sess-1", Tool: "run_command", Decision: "BLOCK"}); err != nil {
		t.Fatalf("Append after reopen failed: %v", err)
	}
	l.Close()

	rep, _ := audit.VerifyFile(path, "")
	if !rep.OK {
		t.Fatalf("Expected the chain to stay valid across reopen, failed at %d: %s", rep.FailedAt, rep.Reason)
	}
	if rep.Count != 4 {
		t.Fatalf("Expected 4 events after reopen+append, got %d", rep.Count)
	}
}

// TestSessionFilterCountsButVerifiesGlobally: filtering must not make an
// untampered chain look broken at a session's first record.
func TestSessionFilterCountsButVerifiesGlobally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, _ := audit.Open(path)
	for _, sess := range []string{"a", "b", "a", "b", "a"} {
		l.Append(audit.Event{SessionID: sess, Tool: "read_file", Decision: "ALLOW"})
	}
	l.Close()

	rep, _ := audit.VerifyFile(path, "a")
	if !rep.OK {
		t.Fatalf("Expected a filtered verify to still pass, failed at %d: %s", rep.FailedAt, rep.Reason)
	}
	if rep.Count != 3 {
		t.Fatalf("Expected 3 events for session a, got %d", rep.Count)
	}
}

func TestHashArgsIsOrderIndependent(t *testing.T) {
	a := audit.HashArgs(map[string]any{"path": "x.go", "depth": 3})
	b := audit.HashArgs(map[string]any{"depth": 3, "path": "x.go"})
	if a != b {
		t.Fatal("Expected argument hashing to be independent of map iteration order")
	}
	if a == audit.HashArgs(map[string]any{"path": "y.go", "depth": 3}) {
		t.Fatal("Expected different arguments to hash differently")
	}
}
