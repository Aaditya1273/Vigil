// Package firewall is Vigil's decision pipeline: the code that decides, for one
// tool call, whether it may proceed.
//
// The shape of Check() is the product's whole thesis. Deterministic checks —
// declared intent, cost forecast, behavioral baseline — run on every call and
// are cheap, explainable, and reproducible. A language model is consulted only
// when those checks are genuinely uncertain, may only make a decision stricter,
// and can never be the reason something was allowed.
package firewall

import (
	"sync"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
)

const (
	// maxSpans bounds the per-session behavioral history.
	// ponytail: 64-span ring; raise if a detector ever needs deeper history.
	// An unbounded log is an OOM waiting for a long-lived Claude Web session,
	// and every current detector only inspects the tail.
	maxSpans = 64
	// maxSamples bounds the cost history used for the burn-rate window.
	maxSamples = 32
)

type sample struct {
	at   time.Time
	cost float64
}

// Session is one agent's running state.
type Session struct {
	mu      sync.Mutex
	id      string
	started time.Time
	cost    float64
	spans   []engine.TraceSpan
	samples []sample
}

func newSession(id string) *Session {
	return &Session{id: id, started: time.Now()}
}

// RecordSpan appends a tool-call span to the bounded history.
func (s *Session) RecordSpan(sp engine.TraceSpan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = append(s.spans, sp)
	if len(s.spans) > maxSpans {
		s.spans = s.spans[len(s.spans)-maxSpans:]
	}
}

// RecordCost appends a cost sample and updates the running total.
func (s *Session) RecordCost(c float64, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cost += c
	s.samples = append(s.samples, sample{at: at, cost: c})
	if len(s.samples) > maxSamples {
		s.samples = s.samples[len(s.samples)-maxSamples:]
	}
}

// Cost returns the session's running spend.
func (s *Session) Cost() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cost
}

// AgentContext builds the input the governance plugins expect.
//
// This is what makes the pre-existing detector plugins reachable: they only
// read Spans, BudgetLimit, and CurrentCost, all of which this fills. The spans
// are copied out under the lock so no plugin can observe a torn slice.
//
// pendingTool, when non-empty, is appended as a prospective span so detectors
// judge the call being *requested* rather than only what already happened.
// This matters more than it looks: the loop detector scans backwards for a run
// of identical tools, so evaluating history alone means a session that has
// looped once refuses every later call regardless of which tool it asks for.
// Including the pending call lets a different tool break the run, which is the
// behavior the detector was always meant to describe.
func (s *Session) AgentContext(budget float64, pendingTool string) *engine.AgentContext {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.spans)
	spans := make([]engine.TraceSpan, n, n+1)
	copy(spans, s.spans)
	if pendingTool != "" {
		spans = append(spans, engine.TraceSpan{Name: pendingTool, Kind: "tool", Status: "ok"})
	}

	return &engine.AgentContext{
		TraceID:     s.id,
		ProjectName: "vigil",
		BudgetLimit: budget,
		CurrentCost: s.cost,
		Spans:       spans,
	}
}

// costSamples copies the cost history for forecasting.
func (s *Session) costSamples() []sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sample, len(s.samples))
	copy(out, s.samples)
	return out
}

// recentTools returns the last n tool names with their statuses, for the
// judge's prompt.
func (s *Session) recentTools(n int) []engine.TraceSpan {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.spans) <= n {
		out := make([]engine.TraceSpan, len(s.spans))
		copy(out, s.spans)
		return out
	}
	out := make([]engine.TraceSpan, n)
	copy(out, s.spans[len(s.spans)-n:])
	return out
}

// Sessions is the per-session state table.
type Sessions struct {
	mu sync.RWMutex
	m  map[string]*Session
}

func newSessions() *Sessions { return &Sessions{m: map[string]*Session{}} }

// Get returns the session for id, creating it on first use.
func (ss *Sessions) Get(id string) *Session {
	ss.mu.RLock()
	s, ok := ss.m[id]
	ss.mu.RUnlock()
	if ok {
		return s
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()
	if s, ok := ss.m[id]; ok { // re-check: another goroutine may have won
		return s
	}
	s = newSession(id)
	ss.m[id] = s
	return s
}

// Drop removes a session's state.
func (ss *Sessions) Drop(id string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.m, id)
}

// Behaviour is a session's observed behavioural profile, assembled from what
// the firewall has actually seen rather than from a ClickHouse baseline.
type Behaviour struct {
	SessionID  string         `json:"session_id"`
	Calls      int            `json:"calls"`
	Cost       float64        `json:"cost"`
	ToolCounts map[string]int `json:"tool_counts"`
	Statuses   map[string]int `json:"statuses"`
	AvgLatency int64          `json:"avg_latency_ms"`
	Observed   bool           `json:"observed"`
}

// Behaviour reports what this session has actually done.
//
// Deliberately derived from the live span ring rather than a ClickHouse
// baseline: the ClickHouse path is not wired in the standalone binary, and
// returning a synthesized fingerprint there would be inventing data. `Observed`
// is false when the session has made no calls, so a caller can tell "nothing
// happened" from "nothing recorded".
func (s *Session) Behaviour() Behaviour {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := Behaviour{
		SessionID:  s.id,
		Cost:       s.cost,
		ToolCounts: map[string]int{},
		Statuses:   map[string]int{},
		Observed:   len(s.spans) > 0,
	}
	var total time.Duration
	for _, sp := range s.spans {
		b.Calls++
		b.ToolCounts[sp.Name]++
		b.Statuses[sp.Status]++
		total += sp.Duration
	}
	if b.Calls > 0 {
		b.AvgLatency = (total / time.Duration(b.Calls)).Milliseconds()
	}
	return b
}
