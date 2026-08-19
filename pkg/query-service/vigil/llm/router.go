package llm

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Tier is how much scrutiny a tool call has earned from deterministic checks.
type Tier int

const (
	// TierNormal: deterministic checks are satisfied. No inference.
	TierNormal Tier = iota
	// TierSuspicious: a low or medium signal fired. Cheap triage.
	TierSuspicious
	// TierUncertain: a high-severity signal fired, or policy could not decide.
	TierUncertain
	// TierHighRisk: reasoning plus an independent security review.
	TierHighRisk
)

func (t Tier) String() string {
	switch t {
	case TierSuspicious:
		return "SUSPICIOUS"
	case TierUncertain:
		return "UNCERTAIN"
	case TierHighRisk:
		return "HIGH_RISK"
	default:
		return "NORMAL"
	}
}

// Stat is per-model usage, for the dashboard's model router panel.
type Stat struct {
	ModelID      string `json:"model_id"`
	Role         string `json:"role"`
	Requests     int    `json:"requests"`
	Failures     int    `json:"failures"`
	Fallbacks    int    `json:"fallbacks"`
	AvgLatency   int64  `json:"avg_latency_ms"`
	TotalTokens  int    `json:"total_tokens"`
	totalLatency time.Duration
}

// Router picks which model roles a tier warrants and records what happened.
type Router struct {
	p      Provider
	logger *slog.Logger

	mu    sync.Mutex
	stats map[string]*Stat
}

func NewRouter(logger *slog.Logger, p Provider) *Router {
	return &Router{p: p, logger: logger, stats: map[string]*Stat{}}
}

// Provider returns the underlying backend name, for the status endpoint.
func (r *Router) Provider() string { return r.p.Name() }

// Vendors reports the failover chain's per-vendor status, or nil when the
// backend is a single provider. The dashboard needs this to show which vendor
// is actually serving and which have been retired — a chain that silently
// degraded to its last member looks identical to a healthy one otherwise.
func (r *Router) Vendors() []map[string]any {
	c, ok := r.p.(*Chain)
	if !ok {
		return nil
	}
	return c.Vendors()
}

// Available reports whether any inference is possible at all.
func (r *Router) Available() bool {
	for _, role := range Roles {
		if r.p.Configured(role) {
			return true
		}
	}
	return false
}

// ConfiguredRoles lists roles that have a model behind them.
func (r *Router) ConfiguredRoles() []string {
	out := []string{}
	for _, role := range Roles {
		if r.p.Configured(role) {
			out = append(out, string(role))
		}
	}
	return out
}

// RolesFor returns the model roles to consult for a tier, in order.
//
// TierNormal returns an empty slice. That is what makes "no inference on the
// happy path" structural rather than a convention someone can forget: there is
// no role to call, so the loop over it does nothing.
func (r *Router) RolesFor(t Tier) []Role {
	switch t {
	case TierSuspicious:
		return []Role{RoleFast}
	case TierUncertain:
		return []Role{RoleReasoner}
	case TierHighRisk:
		return []Role{RoleReasoner, RoleReviewer}
	default:
		return nil
	}
}

// CheaperThan returns a lower-cost role than the given one, for cost-aware
// rerouting when the forecast projects a budget breach.
func CheaperThan(r Role) (Role, bool) { return fallbackRole(r) }

// Complete runs a request through the provider and records usage.
func (r *Router) Complete(ctx context.Context, role Role, req Request) (*Response, error) {
	req.Role = role
	resp, err := r.p.Complete(ctx, req)
	r.record(role, resp, err)
	return resp, err
}

func (r *Router) record(requested Role, resp *Response, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := string(requested)
	if resp != nil && resp.ModelID != "" {
		key = resp.ModelID
	}
	st, ok := r.stats[key]
	if !ok {
		st = &Stat{ModelID: key, Role: string(requested)}
		r.stats[key] = st
	}
	st.Requests++
	if err != nil {
		st.Failures++
		return
	}
	if resp == nil {
		return
	}
	// A response served by a role other than the one asked for means the
	// fallback ladder fired.
	if resp.Role != "" && resp.Role != requested {
		st.Fallbacks++
		st.Role = string(resp.Role)
	}
	st.totalLatency += resp.Latency
	st.AvgLatency = st.totalLatency.Milliseconds() / int64(max(1, st.Requests-st.Failures))
	st.TotalTokens += resp.PromptTokens + resp.CompletionTokens
}

// Stats returns a snapshot of per-model usage.
func (r *Router) Stats() []Stat {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Stat, 0, len(r.stats))
	for _, s := range r.stats {
		out = append(out, *s)
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
