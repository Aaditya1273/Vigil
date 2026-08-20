package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
)

// ErrExhausted means a vendor will not serve this request no matter how many
// times it is retried — out of credits, unauthorised, or key revoked. It is
// distinct from a transient error precisely so the chain can tell "wait and try
// again" from "stop asking this vendor".
var ErrExhausted = errors.New("llm: provider exhausted")

// Vendors, in the order the chain tries them.
//
// Featherless is the hackathon's compute partner and stays first in the
// order for that reason — but the product cannot afford to go
// deterministic-only for however long a Featherless credential takes to
// arrive, so NVIDIA and Gemini are shipped as real, live vendors too, not
// test-only stand-ins (that role was Groq's, kept separate — see
// live_test.go). Each is independently configured from its own env vars;
// configsFromEnv skips whichever ones have no key set, so today, with only
// NVIDIA and Gemini credentials configured, the chain is effectively
// NVIDIA → Gemini, and adding a Featherless key later slots it back in as
// the preferred vendor with no code change. This is failover across
// *vendors*, layered under the existing per-vendor role failover
// (Reviewer → Reasoner → Fast) in openai_compatible.go — cheapest-first
// within a vendor, most-preferred-vendor-first across vendors.
type vendor struct {
	name       string
	envPrefix  string // VIGIL_<prefix>_API_KEY, and per-role model overrides
	defaultURL string
	docs       string
}

var vendors = []vendor{
	{"featherless", "FEATHERLESS", "https://api.featherless.ai/v1", "https://featherless.ai/models"},
	{"nvidia", "NVIDIA", "https://integrate.api.nvidia.com/v1", "https://build.nvidia.com/models"},
	{"gemini", "GEMINI", "https://generativelanguage.googleapis.com/v1beta/openai", "https://ai.google.dev/gemini-api/docs/models"},
}

// quotaPhrases are the bodies vendors return when credit is gone rather than
// when they are merely rate-limiting. Substring matching is crude, but the
// alternative is treating every 429 as terminal and failing over on ordinary
// backpressure, which would waste the primary vendor's capacity.
var quotaPhrases = []string{
	"insufficient", "quota", "credit", "billing", "exceeded your current",
	"payment required", "no credits",
}

func isQuotaExhausted(body []byte) bool {
	l := strings.ToLower(string(body))
	for _, p := range quotaPhrases {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// configsFromEnv builds a client config per configured vendor.
//
// Model IDs still have no defaults. A hardcoded ID that has since been retired
// fails at runtime in production rather than at startup in review, and every
// one of these catalogues churns. VIGIL_MODEL_* sets them for all vendors;
// VIGIL_<VENDOR>_MODEL_* overrides per vendor, because the same role needs a
// different model ID on each.
func configsFromEnv() []Config {
	timeout := 8 * time.Second
	if v := vigil.Env("LLM_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}

	roleEnv := map[Role]string{
		RoleFast:     "MODEL_FAST",
		RoleReasoner: "MODEL_REASONER",
		RoleReviewer: "MODEL_REVIEWER",
	}

	var out []Config
	for _, v := range vendors {
		key := vigil.Env(v.envPrefix + "_API_KEY")
		if key == "" {
			// Accept the bare vendor spelling too: it is what every one of their
			// own docs uses, so an operator copying from there should just work.
			key = osGetenv(v.envPrefix + "_API_KEY")
		}
		if key == "" {
			continue
		}

		models := map[Role]string{}
		for role, suffix := range roleEnv {
			if m := vigil.Env(v.envPrefix + "_" + suffix); m != "" {
				models[role] = m
			} else if m := vigil.Env(suffix); m != "" {
				models[role] = m
			}
		}
		if len(models) == 0 {
			continue
		}

		out = append(out, Config{
			Name:    v.name,
			APIKey:  key,
			BaseURL: vigil.EnvOr(v.envPrefix+"_BASE_URL", v.defaultURL),
			Models:  models,
			Timeout: timeout,
			Retries: 2,
		})
	}
	return out
}

// Chain tries each configured vendor in order and moves on when one is
// exhausted.
//
// This is failover, not load balancing: the order is a preference, and a vendor
// is only skipped once it has actually refused. A vendor that exhausts is
// remembered for the rest of the process, so a chain does not pay the latency
// of asking a dead endpoint on every subsequent call.
type Chain struct {
	logger    *slog.Logger
	providers []*OpenAICompatible

	mu   sync.RWMutex
	dead map[string]string // vendor -> why it was retired
}

// ChainFromEnv builds the failover chain from the environment. It returns nil
// when no vendor is configured, which the caller treats as deterministic-only.
func ChainFromEnv(logger *slog.Logger) *Chain { return NewChain(logger, configsFromEnv()) }

// NewChain builds a failover chain over the given vendor configs, in order.
// It returns nil when none of them is usable.
func NewChain(logger *slog.Logger, cfgs []Config) *Chain {
	var ps []*OpenAICompatible
	for _, cfg := range cfgs {
		p, err := NewOpenAICompatible(logger, cfg)
		if err != nil {
			logger.InfoContext(context.Background(), "llm: vendor not configured",
				slog.String("vendor", cfg.Name), slog.String("detail", err.Error()))
			continue
		}
		ps = append(ps, p)
	}
	if len(ps) == 0 {
		return nil
	}
	return &Chain{logger: logger, providers: ps, dead: map[string]string{}}
}

func (c *Chain) Name() string {
	names := make([]string, 0, len(c.providers))
	for _, p := range c.providers {
		if !c.isDead(p.cfg.Name) {
			names = append(names, p.cfg.Name)
		}
	}
	if len(names) == 0 {
		return "exhausted"
	}
	return strings.Join(names, "→")
}

// Configured reports whether any live vendor can serve this role.
func (c *Chain) Configured(role Role) bool {
	for _, p := range c.providers {
		if !c.isDead(p.cfg.Name) && p.Configured(role) {
			return true
		}
	}
	return false
}

// ConfiguredRoles lists the roles any live vendor can serve.
func (c *Chain) ConfiguredRoles() []string {
	out := make([]string, 0, len(Roles))
	for _, r := range Roles {
		if c.Configured(r) {
			out = append(out, string(r))
		}
	}
	return out
}

// Vendors reports each vendor's status, for the dashboard and /vigil/models.
func (c *Chain) Vendors() []map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]map[string]any, 0, len(c.providers))
	for i, p := range c.providers {
		reason, dead := c.dead[p.cfg.Name]
		roles := make([]string, 0, 3)
		for _, r := range Roles {
			if p.Configured(r) {
				roles = append(roles, string(r))
			}
		}
		out = append(out, map[string]any{
			"vendor":   p.cfg.Name,
			"priority": i + 1,
			"live":     !dead,
			"roles":    roles,
			"retired":  reason, // "" while healthy
		})
	}
	return out
}

// Probe calls every vendor once and retires the ones that refuse.
//
// Without this, `live` in the status endpoint means "a key and a model ID are
// present", which is a claim about configuration dressed up as a claim about
// service. A typo'd key would be reported live until the first agent got
// blocked by it.
//
// The warm connection pool is a side effect worth naming: the first judged
// call would otherwise pay DNS and the TLS handshake out of the firewall's
// decision budget, and on a slow resolver that alone can exhaust it.
//
// Failures other than exhaustion are left alone — a vendor that is briefly
// down at boot should not be written off for the life of the process.
func (c *Chain) Probe(ctx context.Context) {
	for _, p := range c.providers {
		role, ok := c.probeRole(p)
		if !ok {
			continue
		}
		start := time.Now()
		_, err := p.Complete(ctx, Request{
			Role:      role,
			User:      "ping",
			MaxTokens: 1,
		})
		switch {
		case err == nil:
			c.logger.InfoContext(ctx, "llm: vendor reachable",
				slog.String("vendor", p.cfg.Name),
				slog.Duration("latency", time.Since(start).Round(time.Millisecond)))
		case errors.Is(err, ErrExhausted):
			c.retire(p.cfg.Name, err.Error())
		default:
			c.logger.WarnContext(ctx, "llm: vendor probe failed, keeping it in the chain",
				slog.String("vendor", p.cfg.Name), slog.String("error", err.Error()))
		}
	}
}

// probeRole picks the cheapest configured role, so a startup check cannot cost
// real money on the strongest model.
func (c *Chain) probeRole(p *OpenAICompatible) (Role, bool) {
	for _, r := range []Role{RoleFast, RoleReasoner, RoleReviewer} {
		if p.Configured(r) {
			return r, true
		}
	}
	return "", false
}

func (c *Chain) isDead(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, dead := c.dead[name]
	return dead
}

func (c *Chain) retire(name, why string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, already := c.dead[name]; already {
		return
	}
	c.dead[name] = why
	c.logger.WarnContext(context.Background(), "llm: vendor retired for this process",
		slog.String("vendor", name), slog.String("reason", why))
}

// Complete tries each live vendor in order.
//
// A vendor that reports exhaustion is retired and the next is tried
// immediately. Any other failure also moves on, but without retiring: a
// transient 5xx should not permanently cost a vendor its place in the chain.
func (c *Chain) Complete(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	tried := 0

	for _, p := range c.providers {
		if c.isDead(p.cfg.Name) || !p.Configured(req.Role) {
			continue
		}
		tried++

		resp, err := p.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		lastErr = err
		if errors.Is(err, ErrExhausted) {
			c.retire(p.cfg.Name, err.Error())
			continue
		}
		c.logger.WarnContext(ctx, "llm: vendor failed, trying the next",
			slog.String("vendor", p.cfg.Name), slog.String("error", err.Error()))
	}

	if tried == 0 {
		return nil, ErrNoModel
	}
	return nil, fmt.Errorf("llm: every configured vendor failed: %w", lastErr)
}
