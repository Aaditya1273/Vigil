package firewall

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
)

// TestLiveBehaviorDNA is the task's own scenario against the real graph: a
// "normal" behavioral baseline is ingested as plain English (the same way
// every other fact in this product reaches HydraDB — extracted from text,
// never hand-built), then hydraBehaviorCheck is exercised directly with a
// real anomalous tool-call pattern — 19x search_code, 3x network_request —
// the exact shape toolCallSummary produces from a session's real span
// history. Called directly (this file lives in package firewall, not
// firewall_test) rather than through the full Check() pipeline: an earlier
// attempt through Check() found that a pre-existing seeded enterprise
// policy ("read-only-ops permits code-review intent", from Track 01/02
// work) resolves the declared-intent stage to ALLOW before the pipeline
// ever reaches behavioral plugins, which is correct pipeline behavior but
// makes the intent stage a confound for testing behavior-check specifically
// — so this test isolates the one stage the task is actually about.
//
// Skipped unless a key is present, same gating as llm/live_test.go.
//
//	VIGIL_HYDRADB_API_KEY=... go test -run TestLiveBehaviorDNA -v ./pkg/query-service/vigil/firewall/
func TestLiveBehaviorDNA(t *testing.T) {
	key := os.Getenv("VIGIL_HYDRADB_API_KEY")
	if key == "" {
		t.Skip("skipping: VIGIL_HYDRADB_API_KEY unset")
	}
	db := os.Getenv("VIGIL_HYDRADB_DATABASE")
	base := os.Getenv("VIGIL_HYDRADB_BASE_URL")
	if base == "" {
		base = "https://api.hydradb.com"
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := hydra.New(base, key, db, logger)
	ctx := context.Background()

	baseline := "Normal behavioral DNA for a code-review agent: read_file, then search_code, " +
		"then run_tests, in sequence, a handful of times per session. Calling search_code far " +
		"more than a handful of times consecutively, or making unexpected network_request calls " +
		"outside a declared network-access intent, is anomalous behavior, not normal behavioral DNA."
	if _, err := h.IngestMemory(ctx, hydra.CollectionMemory, "behavior-dna-baseline-live-test", baseline); err != nil {
		t.Fatalf("failed to seed behavioral baseline: %v", err)
	}

	f := New(Deps{Logger: logger, Hydra: h})

	agentCtx := &engine.AgentContext{TraceID: "live-behavior-dna"}
	for i := 0; i < 19; i++ {
		agentCtx.Spans = append(agentCtx.Spans, engine.TraceSpan{Name: "search_code", Kind: "tool", Status: "ok"})
	}
	for i := 0; i < 3; i++ {
		agentCtx.Spans = append(agentCtx.Spans, engine.TraceSpan{Name: "network_request", Kind: "tool", Status: "ok"})
	}

	pattern := toolCallSummary(agentCtx)
	if pattern != "search_code x19, network_request x3" {
		t.Fatalf("toolCallSummary produced an unexpected pattern string: %q", pattern)
	}
	t.Logf("querying agent_memory for pattern: %s", pattern)

	gf := f.hydraBehaviorCheck(ctx, Call{SessionID: "live-behavior-dna", Tool: "search_code"}, nil, agentCtx)
	if !gf.resolved {
		t.Fatal("expected hydraBehaviorCheck to find graph signal for a pattern matching the seeded baseline text")
	}
	t.Logf("graph paths: %v", gf.paths)

	// Query text embeds the real pattern, not just an abstract signal name —
	// confirm the response itself, not just plumbing, characterizes it.
	res, err := h.Query(ctx, hydra.CollectionMemory, "memory",
		"Is this pattern anomalous for this agent, or does it match normal behavioral DNA: "+pattern+"?")
	if err != nil {
		t.Fatalf("follow-up behavioral query failed: %v", err)
	}
	joined := strings.ToLower(strings.Join(res.Contexts(), " "))
	if !strings.Contains(joined, "anomal") && !strings.Contains(joined, "unusual") && !strings.Contains(joined, "not normal") {
		t.Errorf("expected the graph's own answer to characterize this pattern as anomalous; got contexts: %v", res.Contexts())
	}
}
