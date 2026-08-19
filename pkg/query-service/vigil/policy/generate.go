package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
)

// Draft is a compiled-but-inactive policy awaiting human confirmation.
type Draft struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Source    string    `json:"source"` // the natural-language input
	Policy    *Policy   `json:"policy"`
	Dangerous []string  `json:"dangerous"` // warnings, not rejections
	ModelUsed string    `json:"model_used"`
	Provider  string    `json:"provider"`
	CreatedAt time.Time `json:"created_at"`
}

const generatorSystemPrompt = `You compile a natural-language agent governance policy into JSON.

Return ONLY a JSON object with exactly these keys:
{
  "declared_intent": string,
  "allowed_tools": [string],
  "denied_tools": [string],
  "allowed_resources": [string],
  "denied_resources": [string],
  "budget_usd": number,
  "risk_tolerance": "LOW" | "MEDIUM" | "HIGH",
  "network_access": boolean,
  "secret_access": boolean
}

Resource categories must be drawn from exactly this set:
filesystem_read, filesystem_write, exec, network, secret, observability

Rules:
- allowed_tools empty means "no tool allowlist", not "deny everything".
- Set network_access false when the policy forbids network use.
- Set secret_access false when the policy forbids credentials or secrets.
- Do not invent keys. Do not include commentary, markdown, or code fences.`

// Generate compiles natural language into a Draft using the reasoning model.
//
// It takes no store and returns an inert Draft. The model therefore has no
// reachable path to activating a policy — that requires a separate, human-
// initiated confirmation. This is structural rather than procedural: there is
// no *Store in scope to mutate.
func Generate(ctx context.Context, router *llm.Router, sessionID, naturalLanguage string) (*Draft, error) {
	if strings.TrimSpace(naturalLanguage) == "" {
		return nil, fmt.Errorf("policy: empty policy description")
	}
	if router == nil || !router.Available() {
		return nil, llm.ErrNoModel
	}

	resp, err := router.Complete(ctx, llm.RoleReasoner, llm.Request{
		System:      generatorSystemPrompt,
		User:        naturalLanguage,
		MaxTokens:   800,
		Temperature: 0,
		JSONOnly:    true,
	})
	if err != nil {
		return nil, err
	}

	pol, dangerous, err := ParseGenerated([]byte(resp.Text), sessionID)
	if err != nil {
		return nil, fmt.Errorf("policy: model output rejected: %w", err)
	}

	return &Draft{
		ID:        fmt.Sprintf("draft-%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Source:    naturalLanguage,
		Policy:    pol,
		Dangerous: dangerous,
		ModelUsed: resp.ModelID,
		Provider:  router.Provider(),
		CreatedAt: time.Now(),
	}, nil
}

// generated is the exact shape accepted from a model. It is a separate type
// from Policy on purpose: server-assigned fields (SessionID, CreatedAt, Active)
// have no representation here, so model output cannot set them at all.
type generated struct {
	DeclaredIntent   string   `json:"declared_intent"`
	AllowedTools     []string `json:"allowed_tools"`
	DeniedTools      []string `json:"denied_tools"`
	AllowedResources []string `json:"allowed_resources"`
	DeniedResources  []string `json:"denied_resources"`
	BudgetUSD        float64  `json:"budget_usd"`
	RiskTolerance    string   `json:"risk_tolerance"`
	NetworkAccess    *bool    `json:"network_access"`
	SecretAccess     *bool    `json:"secret_access"`
}

// ParseGenerated validates model output and compiles it into a Policy.
//
// This is the entire security boundary between a language model and Vigil's
// enforcement state, and it takes bytes rather than calling a model, so every
// rejection path is unit-testable with no credentials.
//
// Order: strip fences, decode strictly, normalize, validate, then flag.
func ParseGenerated(raw []byte, sessionID string) (*Policy, []string, error) {
	cleaned := stripCodeFence(raw)

	var g generated
	dec := json.NewDecoder(bytes.NewReader(cleaned))
	// An unknown field is a rejection, not something to ignore. A model that
	// invented "allow_all": true and had it silently dropped would produce a
	// policy that reads as permissive to its author and is not.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return nil, nil, fmt.Errorf("invalid policy JSON: %w", err)
	}

	// Booleans default to the restrictive value when the model omits them.
	// Absence must not read as permission.
	network := g.NetworkAccess != nil && *g.NetworkAccess
	secret := g.SecretAccess != nil && *g.SecretAccess

	p := &Policy{
		SessionID:        sessionID, // server-assigned, never from the model
		DeclaredIntent:   g.DeclaredIntent,
		AllowedTools:     g.AllowedTools,
		DeniedTools:      g.DeniedTools,
		AllowedResources: g.AllowedResources,
		DeniedResources:  g.DeniedResources,
		BudgetUSD:        g.BudgetUSD,
		RiskTolerance:    Risk(g.RiskTolerance),
		NetworkAccess:    network,
		SecretAccess:     secret,
		CreatedAt:        time.Now(),
		Active:           false, // a draft is inert until a human confirms it
	}
	p.Normalize()
	if err := p.Validate(); err != nil {
		return nil, nil, err
	}

	return p, dangerousRules(p), nil
}

// dangerousRules flags combinations worth a human's attention.
//
// These are warnings, not rejections: an operator may legitimately want a
// permissive session, and refusing to compile it would push them toward
// disabling governance entirely. Surfacing the risk at the confirmation step is
// the useful behavior.
func dangerousRules(p *Policy) []string {
	var out []string

	if p.NetworkAccess && p.SecretAccess {
		out = append(out, "Grants both network and credential access: an agent that can read secrets and reach the network can exfiltrate them.")
	}
	if contains(p.AllowedTools, "run_command") && len(p.DeniedTools) == 0 && len(p.DeniedResources) == 0 {
		out = append(out, "Permits shell execution with no denials declared.")
	}
	if p.RiskTolerance == RiskHigh {
		out = append(out, "HIGH risk tolerance: calls the policy does not cover will be escalated leniently rather than denied.")
	}
	if len(p.AllowedTools) == 0 && len(p.DeniedTools) == 0 {
		out = append(out, "No tool allowlist or denylist: every tool is permitted.")
	}
	if p.BudgetUSD <= 0 {
		out = append(out, "No positive budget set: cost enforcement will treat this session as having nothing to spend.")
	}
	if p.SecretAccess && !contains(p.DeniedResources, CatSecret) {
		out = append(out, "Credential access is permitted.")
	}
	return out
}

// stripCodeFence removes a surrounding markdown fence. Models emit them
// routinely even when told not to, and failing closed over a formatting habit
// is a bad trade when the content itself is still strictly validated.
func stripCodeFence(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(s, "```") {
		return []byte(s)
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return []byte(strings.TrimSpace(s))
}
