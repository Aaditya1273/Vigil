package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/mcp"
)

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.DiscardHandler)
}

func toolCall(name string, args map[string]any) json.RawMessage {
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	req, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  json.RawMessage(params),
	})
	return req
}

// TestConcurrentSessionAccess is the regression test for a hard
// "concurrent map writes" panic: the session table was a bare map written from
// every HTTP handler goroutine. Run under -race.
func TestConcurrentSessionAccess(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.HandleRequest(context.Background(), toolCall("vigil_cost_status", nil), fmt.Sprintf("client-%d", i))
		}(i)
		go func() {
			defer wg.Done()
			_ = s.Sessions()
		}()
	}
	wg.Wait()

	if got := len(s.Sessions()); got != n {
		t.Fatalf("Expected %d sessions, got %d", n, got)
	}
}

// TestSessionCostIsolation pins the fix for per-agent cost attribution. Each
// session must accumulate only its own spend; previously the fleet-wide total
// was reported as every individual agent's cost.
func TestSessionCostIsolation(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	type charge struct {
		agent        string
		sessionTotal float64
	}
	var mu sync.Mutex
	var charges []charge
	s.SetCostCallback(func(agentID string, toolCost, sessionTotal float64, tool string) {
		mu.Lock()
		defer mu.Unlock()
		charges = append(charges, charge{agentID, sessionTotal})
	})

	// "a" makes 3 calls, "b" makes 1.
	for i := 0; i < 3; i++ {
		s.HandleRequest(context.Background(), toolCall("vigil_cost_status", nil), "a")
	}
	s.HandleRequest(context.Background(), toolCall("vigil_cost_status", nil), "b")

	sa, _ := s.Session("a")
	sb, _ := s.Session("b")
	if sa.ToolCallCount != 3 {
		t.Fatalf("Expected session a to have 3 calls, got %d", sa.ToolCallCount)
	}
	if sb.ToolCallCount != 1 {
		t.Fatalf("Expected session b to have 1 call, got %d", sb.ToolCallCount)
	}
	if sb.TotalCost >= sa.TotalCost {
		t.Fatalf("Expected b's cost (%v) to be below a's (%v); a shared accumulator would make them equal", sb.TotalCost, sa.TotalCost)
	}

	// The callback must report the session's own running total, never a global.
	mu.Lock()
	defer mu.Unlock()
	var lastB float64
	for _, c := range charges {
		if c.agent == "b" {
			lastB = c.sessionTotal
		}
	}
	if lastB != sb.TotalCost {
		t.Fatalf("Expected callback sessionTotal %v to match session b's total %v", lastB, sb.TotalCost)
	}
}

// TestBudgetLatchesSessionClosed covers the hard limit: once spend passes the
// budget the session blocks, and every later call short-circuits.
func TestBudgetLatchesSessionClosed(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())
	s.PutSession(mcp.ClientSession{ID: "tight", BudgetLimit: 0.0015}) // ~1 call at $0.001

	for i := 0; i < 3; i++ {
		s.HandleRequest(context.Background(), toolCall("vigil_cost_status", nil), "tight")
	}

	sess, _ := s.Session("tight")
	if !sess.Blocked {
		t.Fatalf("Expected session to be blocked after exceeding budget, got %+v", sess)
	}
	if sess.ToolCallCount > 2 {
		t.Fatalf("Expected calls to stop once blocked, got %d", sess.ToolCallCount)
	}
}

// TestFirewallBlocksWithoutCharging pins the ordering fix: a refused call must
// neither execute nor be billed. The pre-2.0 path charged first, so a blocked
// call still moved the session's cost.
func TestFirewallBlocksWithoutCharging(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	var committed int
	s.SetFirewall(
		func(ctx context.Context, in mcp.FirewallInput) mcp.FirewallVerdict {
			return mcp.FirewallVerdict{Allow: false, Decision: "BLOCK", Reason: "test", Message: "blocked by test"}
		},
		func(sessionID, tool string, cost float64, dur time.Duration, ok bool) { committed++ },
	)

	resp := s.HandleRequest(context.Background(), toolCall("vigil_cost_status", nil), "c")
	if resp == nil {
		t.Fatal("Expected a response")
	}

	sess, _ := s.Session("c")
	if sess.TotalCost != 0 {
		t.Fatalf("Expected no charge for a blocked call, got %v", sess.TotalCost)
	}
	if sess.ToolCallCount != 0 {
		t.Fatalf("Expected no call recorded for a blocked call, got %d", sess.ToolCallCount)
	}
	if committed != 0 {
		t.Fatalf("Expected commit not to run for a blocked call, ran %d times", committed)
	}
}

// TestNoFirewallPreservesLegacyBehavior: with no hook installed the server must
// behave exactly as it did before 2.0.
func TestNoFirewallPreservesLegacyBehavior(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())
	s.HandleRequest(context.Background(), toolCall("vigil_cost_status", nil), "d")

	sess, _ := s.Session("d")
	if sess.ToolCallCount != 1 {
		t.Fatalf("Expected the call to run with no firewall installed, got %d calls", sess.ToolCallCount)
	}
	if sess.TotalCost <= 0 {
		t.Fatalf("Expected the call to be charged, got %v", sess.TotalCost)
	}
}

// TestDemoLabelingComesFromClient: demo labeling must key off the client's own
// declared name, so a real agent connecting during a demo is not mislabeled.
func TestDemoLabelingComesFromClient(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	init := func(clientName string) json.RawMessage {
		params, _ := json.Marshal(map[string]any{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]any{"name": clientName, "version": "1"},
		})
		req, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": json.RawMessage(params),
		})
		return req
	}

	s.HandleRequest(context.Background(), init(mcp.DemoClientPrefix+" Harness"), "demo-sess")
	s.HandleRequest(context.Background(), init("Claude Web"), "real-sess")

	if d, _ := s.Session("demo-sess"); !d.Demo {
		t.Fatal("Expected the demo harness session to be labeled demo")
	}
	if r, _ := s.Session("real-sess"); r.Demo {
		t.Fatal("Expected a real client session not to be labeled demo")
	}
}
