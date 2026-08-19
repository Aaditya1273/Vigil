package llm_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
)

// namedCfg is cfg() with a vendor name, so assertions can tell which endpoint
// actually served a request.
func namedCfg(name, base string) llm.Config {
	return llm.Config{
		Name:    name,
		APIKey:  "test-key",
		BaseURL: base,
		Models:  map[llm.Role]string{llm.RoleFast: name + "/model"},
		Timeout: 2 * time.Second,
		Retries: 1,
	}
}

// TestChainFailsOverOnExhaustedCredit is the behaviour the chain exists for:
// the primary vendor is out of credit, so the request is served by the next.
func TestChainFailsOverOnExhaustedCredit(t *testing.T) {
	primary, primaryCalls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"error":{"message":"insufficient credits"}}`))
	})
	backup, backupCalls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okBody("backup/served", `{"ok":true}`)))
	})

	c := llm.NewChain(newTestLogger(t), []llm.Config{
		namedCfg("primary", primary),
		namedCfg("backup", backup),
	})
	if c == nil {
		t.Fatal("Expected a chain from two valid configs")
	}

	resp, err := c.Complete(context.Background(), llm.Request{Role: llm.RoleFast, User: "hi"})
	if err != nil {
		t.Fatalf("Expected failover to succeed, got %v", err)
	}
	if resp.ModelID != "backup/served" {
		t.Errorf("Expected the backup to serve, got model %q", resp.ModelID)
	}

	// A 402 must not be retried: it will not become correct on a second ask.
	if got := primaryCalls.Load(); got != 1 {
		t.Errorf("Expected the exhausted vendor to be called exactly once, got %d", got)
	}
	if got := backupCalls.Load(); got != 1 {
		t.Errorf("Expected the backup to be called once, got %d", got)
	}

	// The primary is retired for the process, so the next call skips it
	// entirely rather than paying its latency again.
	if _, err := c.Complete(context.Background(), llm.Request{Role: llm.RoleFast, User: "again"}); err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	if got := primaryCalls.Load(); got != 1 {
		t.Errorf("Expected the retired vendor to be skipped, but it was called %d times", got)
	}
}

// TestChainFailsOverOnQuota429 covers the ambiguous status: a 429 whose body
// says "out of credits" is terminal, so the chain must move on.
func TestChainFailsOverOnQuota429(t *testing.T) {
	primary, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"You exceeded your current quota"}}`))
	})
	backup, backupCalls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okBody("backup/served", "ok")))
	})

	c := llm.NewChain(newTestLogger(t), []llm.Config{
		namedCfg("primary", primary),
		namedCfg("backup", backup),
	})
	if _, err := c.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err != nil {
		t.Fatalf("Expected failover on a quota 429, got %v", err)
	}
	if backupCalls.Load() != 1 {
		t.Error("Expected the backup to serve the request")
	}
}

// TestChainDoesNotRetireOnBackpressure is the other half of that distinction.
// An ordinary 429 is transient: the vendor keeps its place in the chain so a
// brief burst does not cost it the primary slot for the rest of the process.
func TestChainDoesNotRetireOnBackpressure(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	primary, primaryCalls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"message":"rate limit reached, please slow down"}}`))
			return
		}
		w.Write([]byte(okBody("primary/served", "ok")))
	})
	backup, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okBody("backup/served", "ok")))
	})

	c := llm.NewChain(newTestLogger(t), []llm.Config{
		namedCfg("primary", primary),
		namedCfg("backup", backup),
	})

	// First call: the primary is rate-limited, retries, then the backup serves.
	if _, err := c.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err != nil {
		t.Fatalf("First call failed: %v", err)
	}
	if primaryCalls.Load() < 2 {
		t.Errorf("Expected a plain 429 to be retried, got %d calls", primaryCalls.Load())
	}

	// The burst passes. The primary must still be tried first.
	fail.Store(false)
	resp, err := c.Complete(context.Background(), llm.Request{Role: llm.RoleFast})
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	if resp.ModelID != "primary/served" {
		t.Errorf("Expected the primary to keep its slot after backpressure, got %q", resp.ModelID)
	}
}

// TestChainReportsNoModelWhenNoVendorServesTheRole guards the fail-closed
// contract: an unservable role must surface ErrNoModel, which every caller
// already treats as "no judgement available", not as an allow.
func TestChainReportsNoModelWhenNoVendorServesTheRole(t *testing.T) {
	base, calls := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okBody("m", "ok")))
	})
	c := llm.NewChain(newTestLogger(t), []llm.Config{namedCfg("only", base)})

	// Only RoleFast is configured, and fallback is downward-only, so a reviewer
	// request degrades rather than failing. Ask for the one that cannot degrade.
	if _, err := c.Complete(context.Background(), llm.Request{Role: "nonexistent"}); err == nil {
		t.Fatal("Expected an error for an unconfigured role")
	}
	if calls.Load() != 0 {
		t.Errorf("Expected no HTTP call for an unconfigured role, got %d", calls.Load())
	}
}

// TestChainNameListsLiveVendors is what the dashboard and audit record show, so
// a retired vendor must stop appearing as if it were serving traffic.
func TestChainNameListsLiveVendors(t *testing.T) {
	dead, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`))
	})
	live, _ := fakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(okBody("live/served", "ok")))
	})

	c := llm.NewChain(newTestLogger(t), []llm.Config{namedCfg("dead", dead), namedCfg("live", live)})
	if !strings.Contains(c.Name(), "dead") {
		t.Errorf("Expected both vendors before any call, got %q", c.Name())
	}
	if _, err := c.Complete(context.Background(), llm.Request{Role: llm.RoleFast}); err != nil {
		t.Fatalf("Expected failover past a bad key, got %v", err)
	}
	if strings.Contains(c.Name(), "dead") {
		t.Errorf("Expected the retired vendor to drop out of the name, got %q", c.Name())
	}

	vendors := c.Vendors()
	if len(vendors) != 2 {
		t.Fatalf("Expected both vendors to still be reported, got %d", len(vendors))
	}
	if vendors[0]["live"] != false {
		t.Error("Expected the retired vendor to report live=false")
	}
	if vendors[1]["live"] != true {
		t.Error("Expected the healthy vendor to report live=true")
	}
}

// TestNewChainNilWhenNothingConfigured is what makes deterministic-only mode
// reachable: no credential anywhere must not produce an empty chain that fails
// every call.
func TestNewChainNilWhenNothingConfigured(t *testing.T) {
	if c := llm.NewChain(newTestLogger(t), nil); c != nil {
		t.Error("Expected nil from an empty config list")
	}
	if c := llm.NewChain(newTestLogger(t), []llm.Config{{Name: "x", BaseURL: "http://x"}}); c != nil {
		t.Error("Expected nil when the only vendor has no key")
	}
}
