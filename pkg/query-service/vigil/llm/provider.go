// Package llm provides Vigil's inference layer: a small OpenAI-compatible
// client for Featherless, and a router that decides which model role — if any —
// a given risk tier warrants.
//
// The central rule is that inference is optional. Deterministic checks decide
// the common case; a model is consulted only when they are genuinely uncertain.
// When no credentials are configured, every call fails with ErrNoModel and the
// caller takes its deterministic path. Nothing here ever fabricates a verdict.
package llm

import (
	"context"
	"errors"
	"time"
)

// Role identifies what a model is being asked to do. Each role maps to a
// separately configured model so cost can be matched to the difficulty of the
// question.
type Role string

const (
	// RoleFast triages calls that deterministic checks flagged but could not
	// resolve. Should be the cheapest, lowest-latency model available.
	RoleFast Role = "FAST_RISK_CLASSIFIER"
	// RoleReasoner handles genuinely ambiguous decisions and compiles
	// natural-language policy into structured form.
	RoleReasoner Role = "POLICY_REASONER"
	// RoleReviewer is the last word on calls already judged high risk.
	RoleReviewer Role = "DEEP_SECURITY_REVIEWER"
)

// Roles in escalation order, cheapest first.
var Roles = []Role{RoleFast, RoleReasoner, RoleReviewer}

// Request is one completion request.
type Request struct {
	Role        Role
	System      string
	User        string
	MaxTokens   int
	Temperature float64
	// JSONOnly asks the provider for a JSON object. Callers must still validate
	// the response: this is a hint to the model, not a guarantee.
	JSONOnly bool
}

// Response is what a provider returned, plus what it cost to get it.
type Response struct {
	Text string
	// ModelID is the model that actually served the request, read from the
	// response body rather than echoed from the request — after a fallback
	// those differ, and the audit record must name the one that really ran.
	ModelID          string
	RequestID        string
	Latency          time.Duration
	PromptTokens     int
	CompletionTokens int
	// Role is the role whose model served the request. Differs from the
	// requested role when a fallback fired.
	Role Role
}

// Provider is an inference backend.
type Provider interface {
	// Name identifies the backend for logs, telemetry, and the dashboard.
	Name() string
	// Configured reports whether a model is available for a role.
	Configured(role Role) bool
	// Complete runs one request. It must return an error rather than a
	// synthesized answer when it cannot reach a model.
	Complete(ctx context.Context, req Request) (*Response, error)
}

// ErrNoModel means no model is configured or reachable for the requested role.
// Callers treat this exactly like a timeout or a malformed response: fall back
// to deterministic rules.
var ErrNoModel = errors.New("llm: no model configured for role")
