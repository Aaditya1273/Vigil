// Package policy turns a session's declared intent into an enforceable,
// explainable rule set.
//
// The unit of governance is the session, not the tool: an agent told to "fix
// the failing tests, no network, $2" should be judged against that sentence for
// its whole life. A tool call that would be fine under one intent is a
// violation under another, which a static per-tool allowlist cannot express.
package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Risk is how much benefit of the doubt an ambiguous call gets.
type Risk string

const (
	RiskLow    Risk = "LOW"    // ambiguity is denied
	RiskMedium Risk = "MEDIUM" // ambiguity is escalated for judgement
	RiskHigh   Risk = "HIGH"   // ambiguity is escalated, judged leniently
)

// Policy is the compiled form of a session's declared intent.
type Policy struct {
	SessionID      string `json:"session_id"`
	DeclaredIntent string `json:"declared_intent"`

	// AllowedTools, when non-empty, is an allowlist. Empty means "no tool
	// allowlist" — every tool is permitted unless denied. This distinction is
	// load-bearing: an empty allowlist must not silently deny everything, or
	// a policy that only sets DeniedTools would block the whole session.
	AllowedTools []string `json:"allowed_tools"`
	DeniedTools  []string `json:"denied_tools"`

	AllowedResources []string `json:"allowed_resources"`
	DeniedResources  []string `json:"denied_resources"`

	BudgetUSD     float64 `json:"budget_usd"`
	RiskTolerance Risk    `json:"risk_tolerance"`
	NetworkAccess bool    `json:"network_access"`
	SecretAccess  bool    `json:"secret_access"`

	CreatedAt time.Time `json:"created_at"`
	// Active distinguishes an enforcing policy from a draft awaiting a human.
	Active bool `json:"active"`
}

// Default is the policy applied to a session that never declared an intent.
//
// It permits everything and denies nothing, so existing MCP clients behave
// exactly as they did before intent enforcement existed. Intent is opt-in; a
// deny-by-default here would break every current integration on upgrade.
func Default(sessionID string, budget float64) *Policy {
	return &Policy{
		SessionID:     sessionID,
		BudgetUSD:     budget,
		RiskTolerance: RiskMedium,
		NetworkAccess: true,
		SecretAccess:  true,
		CreatedAt:     time.Now(),
		Active:        true,
	}
}

// IsDefault reports whether this policy is the permissive baseline rather than
// a declared intent, so the dashboard can say so instead of implying
// enforcement that is not happening.
func (p *Policy) IsDefault() bool {
	return p.DeclaredIntent == "" && len(p.AllowedTools) == 0 && len(p.DeniedTools) == 0 &&
		len(p.DeniedResources) == 0 && p.NetworkAccess && p.SecretAccess
}

// Normalize canonicalises a policy in place: trims and lowercases names,
// removes duplicates and empties, and clamps the budget.
func (p *Policy) Normalize() {
	p.AllowedTools = cleanList(p.AllowedTools)
	p.DeniedTools = cleanList(p.DeniedTools)
	p.AllowedResources = cleanList(p.AllowedResources)
	p.DeniedResources = cleanList(p.DeniedResources)

	p.RiskTolerance = Risk(strings.ToUpper(strings.TrimSpace(string(p.RiskTolerance))))
	if p.RiskTolerance == "" {
		p.RiskTolerance = RiskMedium
	}
	if p.BudgetUSD < 0 {
		p.BudgetUSD = 0
	}
	p.DeclaredIntent = strings.TrimSpace(p.DeclaredIntent)
}

// Validate reports why a policy cannot be enforced.
func (p *Policy) Validate() error {
	switch p.RiskTolerance {
	case RiskLow, RiskMedium, RiskHigh:
	default:
		return fmt.Errorf("risk_tolerance must be LOW, MEDIUM, or HIGH, got %q", p.RiskTolerance)
	}

	for _, c := range append(append([]string{}, p.AllowedResources...), p.DeniedResources...) {
		if !validCategory(c) {
			return fmt.Errorf("unknown resource category %q (valid: %s)", c, strings.Join(AllCategories, ", "))
		}
	}

	// A tool in both lists is a contradiction. Silently resolving it either way
	// hides an authoring mistake in a security rule.
	denied := map[string]bool{}
	for _, d := range p.DeniedTools {
		denied[d] = true
	}
	for _, a := range p.AllowedTools {
		if denied[a] {
			return fmt.Errorf("tool %q appears in both allowed_tools and denied_tools", a)
		}
	}

	if p.BudgetUSD < 0 {
		return errors.New("budget_usd must not be negative")
	}
	return nil
}

func validCategory(c string) bool {
	for _, known := range AllCategories {
		if c == known {
			return true
		}
	}
	return false
}

func cleanList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Clone returns a deep copy, so a stored policy cannot be mutated by a caller
// holding a reference to it.
func (p *Policy) Clone() *Policy {
	if p == nil {
		return nil
	}
	cp := *p
	cp.AllowedTools = append([]string(nil), p.AllowedTools...)
	cp.DeniedTools = append([]string(nil), p.DeniedTools...)
	cp.AllowedResources = append([]string(nil), p.AllowedResources...)
	cp.DeniedResources = append([]string(nil), p.DeniedResources...)
	return &cp
}
