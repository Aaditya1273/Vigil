package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/audit"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/recovery"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/telemetry"
)

// Decision is the firewall's answer for one tool call.
type Decision string

const (
	Allow    Decision = "ALLOW"
	Pause    Decision = "PAUSE"
	Block    Decision = "BLOCK"
	Fallback Decision = "FALLBACK"
)

// Stage names which check produced the decision, so the dashboard can show
// *why* rather than just *what*.
const (
	StageIntent    = "intent"
	StageForecast  = "forecast"
	StageBehavior  = "behavior"
	StageCodeGraph = "code_graph"
	StageJudge     = "judge"
	StageDefault   = "default"
)

// Call is one tool call awaiting a decision.
type Call struct {
	SessionID   string
	Tool        string
	Args        map[string]any
	ToolCost    float64
	SessionCost float64
	Budget      float64
}

// Result is a decision plus everything needed to explain and audit it.
type Result struct {
	Decision  Decision `json:"decision"`
	Stage     string   `json:"stage"`
	Reason    string   `json:"reason"`
	RuleName  string   `json:"rule_name,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	Tool      string   `json:"tool"`
	SessionID string   `json:"session_id"`
	// RiskScore is -1 when no model was consulted. Zero would be
	// indistinguishable from "a model looked and saw no risk".
	RiskScore int       `json:"risk_score"`
	ModelUsed string    `json:"model_used,omitempty"`
	Signals   []string  `json:"signals,omitempty"`
	Cost      float64   `json:"cost"`
	Forecast  Snapshot  `json:"forecast"`
	Message   string    `json:"message,omitempty"`
	At        time.Time `json:"at"`

	// GraphPaths is the set of (entity)--[relation]-->(entity) edges HydraDB
	// actually traversed to inform this decision. Empty when HydraDB was not
	// configured, was not consulted for this call (a clearly-covered,
	// uncontested call never reaches the graph — see Check()'s ordering), or
	// had nothing relevant in the graph. Never fabricated: this is exactly the
	// EntityPaths() a real query returned, or nothing.
	GraphPaths []string `json:"graph_paths,omitempty"`
	// GraphQueried records that a graph query round-trip actually happened for
	// this decision, whether or not it resolved anything — the dashboard and
	// the acceptance tests use this to prove HydraDB was on the request path,
	// not merely configured.
	GraphQueried bool `json:"graph_queried"`
	// SupplyChain is populated only when a call was blocked by the
	// compromised-package list (supply_chain.go) — the structured
	// exposed-services/maintainer/typosquat report an incident responder
	// actually needs, not just "blocked".
	SupplyChain *SupplyChainReport `json:"supply_chain,omitempty"`
}

// Deps are the firewall's collaborators. All are optional except Policies:
// a deployment with no ledger, no model, and no governance engine still
// enforces intent and cost.
type Deps struct {
	Logger     *slog.Logger
	Policies   *policy.Store
	Gov        *engine.GovernanceEngine
	Heal       *recovery.SelfHealingEngine
	Router     *llm.Router
	Ledger     *audit.Ledger
	Forecaster Forecaster
	// Hydra is the graph-native context layer. A nil *hydra.Client is a safe
	// no-op everywhere it's used (see hydra.go) — a deployment with no
	// HydraDB credential still enforces intent, cost, and behavior; it just
	// does so without the graph consult, same posture as Router being nil.
	Hydra *hydra.Client
	// Compromised is the incident-response denylist checked before the
	// general typosquat heuristic. Nil (or empty) is the normal state — no
	// active incident. See supply_chain.go.
	Compromised *CompromisedList
	// RecentSize bounds the in-memory decision ring served to the dashboard.
	RecentSize int
}

// Firewall is the decision pipeline.
type Firewall struct {
	deps     Deps
	sessions *Sessions

	mu     sync.Mutex
	recent []Result
}

func New(d Deps) *Firewall {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Policies == nil {
		d.Policies = policy.NewStore()
	}
	if d.Forecaster.HardLimitPct == 0 {
		d.Forecaster = DefaultForecaster()
	}
	if d.RecentSize <= 0 {
		d.RecentSize = 200
	}
	return &Firewall{deps: d, sessions: newSessions()}
}

// Policies exposes the policy store for the HTTP layer.
func (f *Firewall) Policies() *policy.Store { return f.deps.Policies }

// Router exposes the model router for the status endpoint.
func (f *Firewall) Router() *llm.Router { return f.deps.Router }

// Check decides whether a tool call may proceed.
//
// The ordering IS the security guarantee, so it is written as one readable
// sequence:
//
//  1. Declared intent. A BLOCK here returns immediately and is final. An
//     UNCERTAIN verdict now asks HydraDB's enterprise knowledge graph before
//     it's allowed to escalate toward a model — a graph traversal, not a
//     keyword match, decides whether policy actually covers this call.
//  2. Blast radius. Any install/exec-shaped call is checked against the
//     code_graph collection unconditionally, not gated behind a tier — a
//     typosquat or a package with a wide reverse-dependency closure blocks
//     here regardless of how "normal" everything else about the call looks.
//  3. Cost forecast on the *projected* charge. Hard limit blocks; soft limit
//     does not block, it recommends a cheaper route and raises scrutiny.
//  4. Behavioral plugins. CRITICAL blocks; HIGH and below raise scrutiny, and
//     any raised signal is checked against agent_memory for behavioral drift
//     — has this session's agent done this before, or is it new.
//  5. If nothing raised scrutiny, allow — with no model call, and no graph
//     call either: a call fully covered by declared intent never needed
//     either.
//  6. Only now, a model — Featherless — and only when the graph itself
//     couldn't resolve the uncertainty. Graph first, model second: a model
//     can never be the reason something was allowed, and now neither can it
//     be consulted before the graph had its say.
func (f *Firewall) Check(ctx context.Context, c Call) Result {
	ctx, span := telemetry.StartDecision(ctx, c.SessionID, c.Tool)
	defer span.End()

	sess := f.sessions.Get(c.SessionID)
	pol := f.deps.Policies.GetOrDefault(c.SessionID, c.Budget)

	res := Result{
		Tool:      c.Tool,
		SessionID: c.SessionID,
		RiskScore: -1,
		Cost:      c.ToolCost,
		At:        time.Now(),
	}

	// --- 1. Declared intent, graph-adjudicated when uncertain -----------------
	verdict := pol.Evaluate(c.Tool, c.Args)
	if verdict.Outcome == policy.Block {
		res.Decision, res.Stage, res.Reason = Block, StageIntent, verdict.Reason
		res.Message = "Vigil blocked this call: " + verdict.Reason
		return f.finish(ctx, span, sess, res)
	}
	if verdict.Outcome == policy.Uncertain {
		graphVerdict, gf := f.hydraIntentCheck(ctx, c)
		if gf.resolved {
			res.GraphQueried = true
			res.GraphPaths = append(res.GraphPaths, gf.paths...)
		}
		switch graphVerdict {
		case "deny":
			res.Decision, res.Stage = Block, StageIntent
			res.Reason = "denied by enterprise policy graph: " + strings.Join(gf.paths, "; ")
			res.Message = "Vigil blocked this call: " + res.Reason
			return f.finish(ctx, span, sess, res)
		case "allow":
			// The graph resolved the ambiguity a keyword allowlist couldn't:
			// leave the outcome ALLOW-eligible, and do not force an escalation
			// tier bump purely on "intent_uncovered" — that signal's whole
			// purpose was to hand the question to something that can look
			// beyond the literal tool name, and the graph just did.
			verdict.Reason = "permitted by enterprise policy graph: " + strings.Join(gf.paths, "; ")
		}
		// "uncertain" or an unreachable graph: fall through unchanged. The
		// existing intent_uncovered signal (below) still escalates the tier,
		// and Featherless — not the graph — gets the next attempt.
	}

	// --- 2. Blast radius, unconditional for install/exec-shaped calls --------
	if pkg := blastRadiusTarget(c); pkg != "" {
		// A package already on the incident-response denylist is blocked
		// without waiting on the typosquat heuristic below — an operator who
		// has confirmed a package is compromised already knows the answer,
		// the graph query is for the exposure report, not the decision.
		if blocked, report, name := f.checkSupplyChain(ctx, c); blocked {
			res.Decision, res.Stage = Block, StageCodeGraph
			res.SupplyChain = report
			res.Reason = "package " + name + " is on the compromised-package list"
			if report != nil {
				res.GraphQueried = true
				res.Reason += fmt.Sprintf(" — %d service(s) exposed, %d shared maintainer(s), %d typosquat(s) found in %.0fms",
					len(report.ExposedServices), len(report.MaintainerShared), len(report.Typosquats), report.BlastRadiusTimeMS)
			}
			res.Message = "Vigil blocked this call: " + res.Reason
			return f.finish(ctx, span, sess, res)
		}

		highRisk, gf := f.hydraBlastRadius(ctx, pkg)
		if gf.resolved {
			res.GraphQueried = true
			res.GraphPaths = append(res.GraphPaths, gf.paths...)
		}
		if highRisk {
			res.Decision, res.Stage = Block, StageCodeGraph
			res.Reason = fmt.Sprintf("package %s flagged by dependency/maintainer graph: %s", pkg, strings.Join(gf.paths, "; "))
			res.Message = "Vigil blocked this call: " + res.Reason
			return f.finish(ctx, span, sess, res)
		}
	}

	// --- 3. Cost forecast ----------------------------------------------------
	// Forecast the charge this call *would* incur, not the one already spent —
	// the point is to intervene before the budget is gone.
	fc := f.deps.Forecaster.Compute(time.Now(), c.SessionCost+c.ToolCost, c.Budget, sess.costSamples())
	res.Forecast = fc

	tier := llm.TierNormal
	var signals []string

	if fc.State == StateHardLimit {
		res.Decision, res.Stage = Block, StageForecast
		res.Reason = fmt.Sprintf("budget exhausted: $%.4f of $%.2f", fc.CurrentCost, fc.Budget)
		res.Message = "Vigil blocked this call: " + res.Reason
		return f.finish(ctx, span, sess, res)
	}
	if fc.State == StateSoftLimit {
		// Not a block. A projected breach should reroute to something cheaper,
		// not kill an agent that is still doing useful work.
		signals = append(signals, "cost_soft_limit")
		tier = maxTier(tier, llm.TierSuspicious)
		f.recover(ctx, sess, c, engine.RuleResult{
			RuleName:        "Projected Budget Breach",
			Reason:          fmt.Sprintf("projected $%.4f against a $%.2f budget", fc.ProjectedTotal, fc.Budget),
			Severity:        engine.SeverityMedium,
			AutomaticAction: engine.ActionTriggerFallback,
		})
	}

	// --- 4. Behavioral plugins, checked against agent_memory when raised -----
	if f.deps.Gov != nil {
		agentCtx := sess.AgentContext(c.Budget, c.Tool)
		for _, v := range f.deps.Gov.EvaluateContext(ctx, agentCtx) {
			signals = append(signals, v.RuleName)
			switch v.Severity {
			case engine.SeverityCritical:
				res.Decision, res.Stage = Block, StageBehavior
				res.RuleName, res.Severity, res.Reason = v.RuleName, string(v.Severity), v.Reason
				res.Signals = signals
				res.Message = "Vigil blocked this call: " + v.Reason
				return f.finish(ctx, span, sess, res)
			case engine.SeverityHigh:
				tier = maxTier(tier, llm.TierUncertain)
				res.RuleName, res.Severity = v.RuleName, string(v.Severity)
			default:
				tier = maxTier(tier, llm.TierSuspicious)
			}
		}
	}

	// Policy could not decide, and the graph didn't resolve it either: exactly
	// the case a model is for. If the graph already turned this into "allow"
	// above, verdict.Outcome is still technically Uncertain but the reason now
	// carries the graph's answer — this signal, and the escalation it causes,
	// only fires when neither the deterministic policy nor the graph resolved it.
	if verdict.Outcome == policy.Uncertain && !strings.Contains(verdict.Reason, "policy graph") {
		signals = append(signals, "intent_uncovered")
		tier = maxTier(tier, llm.TierUncertain)
	}
	res.Signals = signals

	// A signal was already raised by something above: ask agent_memory whether
	// this pattern is familiar before deciding whether to escalate further.
	// Not run when nothing raised scrutiny — the whole point is to reserve the
	// graph call for calls that are already in question.
	if len(signals) > 0 {
		gf := f.hydraBehaviorCheck(ctx, c, signals)
		if gf.resolved {
			res.GraphQueried = true
			res.GraphPaths = append(res.GraphPaths, gf.paths...)
		}
	}

	// --- 5. Nothing raised scrutiny -----------------------------------------
	if tier == llm.TierNormal {
		res.Decision, res.Stage, res.Reason = Allow, StageDefault, verdict.Reason
		return f.finish(ctx, span, sess, res)
	}

	// --- 6. Model judgement, to tighten only, graph already had its say ------
	if f.deps.Router == nil || !f.deps.Router.Available() {
		// No inference configured, so nothing can adjudicate the uncertainty.
		//
		// An UNCERTAIN intent verdict means the call fell outside a declared
		// allowlist. Allowing it here would make `allowed_tools` advisory on the
		// default configuration -- the operator wrote an allowlist and got a
		// suggestion. It fails closed instead, and says why.
		//
		// Other signals (a soft cost limit, a MEDIUM-severity detector) are
		// warnings rather than exclusions, so those still pass with the signal
		// recorded. Blocking on every signal when the provider is merely
		// unconfigured would make an unconfigured deployment unusable.
		if verdict.Outcome == policy.Uncertain {
			res.Decision, res.Stage = Block, StageIntent
			res.Reason = verdict.Reason + " (no judge configured to adjudicate it)"
			res.Signals = signals
			res.Message = "Vigil blocked this call: " + res.Reason
			return f.finish(ctx, span, sess, res)
		}
		res.Decision, res.Stage = Allow, StageDefault
		res.Reason = deterministicReason(signals, verdict.Reason)
		return f.finish(ctx, span, sess, res)
	}

	j, model, err := f.consult(ctx, tier, c, pol, sess, signals)
	if err != nil {
		// Fail closed on the strength of what determinism already found: a HIGH
		// signal we could not adjudicate becomes a block, anything less does
		// not. Never allow *because* the model failed.
		res.Stage = StageJudge
		res.Reason = fmt.Sprintf("model judgement unavailable (%v); deterministic signals: %s", err, strings.Join(signals, ", "))
		if tier >= llm.TierUncertain {
			res.Decision = Block
			res.Message = "Vigil blocked this call: " + res.Reason
		} else {
			res.Decision = Allow
		}
		return f.finish(ctx, span, sess, res)
	}

	res.Stage, res.ModelUsed, res.RiskScore = StageJudge, model, j.score()
	res.Severity = j.Severity
	res.Reason = j.reason()

	switch Decision(j.Decision) {
	case Block:
		res.Decision, res.Message = Block, "Vigil blocked this call: "+res.Reason
	case Pause:
		res.Decision, res.Message = Pause, "Vigil paused this session for review: "+res.Reason
	case Fallback:
		// Vigil does not run the agent's model, so it cannot execute a fallback
		// on the agent's behalf. The call proceeds and the recommendation is
		// surfaced; pretending otherwise would be a fake feature.
		res.Decision = Allow
		res.Reason = "model recommended a cheaper route: " + res.Reason
	default:
		res.Decision = Allow
	}
	return f.finish(ctx, span, sess, res)
}

// judgeBudget bounds the entire escalation stage.
//
// The per-attempt timeout does not bound this: a HighRisk tier walks two roles,
// each re-prompts once on a schema slip, each of those retries transient
// failures, and the chain repeats the whole thing per vendor. Multiplied out
// that is minutes, while the HTTP server's WriteTimeout is 15s — so the agent
// would get a dropped connection instead of a decision, which is a fail-open
// dressed as a network error.
//
// Ten seconds leaves headroom under WriteTimeout. Exceeding it is not an
// outage: the deadline surfaces as an error on the existing fail-closed path,
// so the call is decided on deterministic signals alone.
var judgeBudget = func() time.Duration {
	if v := vigil.Env("JUDGE_BUDGET_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 10 * time.Second
}()

// consult asks the model, retrying once on a validation failure.
func (f *Firewall) consult(ctx context.Context, tier llm.Tier, c Call, pol *policy.Policy, sess *Session, signals []string) (judgment, string, error) {
	ctx, cancel := context.WithTimeout(ctx, judgeBudget)
	defer cancel()

	prompt := f.buildPrompt(c, pol, sess, signals)

	var lastErr error
	var lastModel string
	for _, role := range f.deps.Router.RolesFor(tier) {
		if ctx.Err() != nil {
			// Out of budget. Stop rather than walking the remaining roles to
			// collect the same deadline error from each.
			return judgment{}, lastModel, ctx.Err()
		}
		user := prompt
		for attempt := 0; attempt < 2; attempt++ {
			mctx, mspan := telemetry.StartModelCall(ctx, string(role))
			resp, err := f.deps.Router.Complete(mctx, role, llm.Request{
				System: judgeSystemPrompt, User: user, MaxTokens: 500, Temperature: 0, JSONOnly: true,
			})
			var mc telemetry.ModelCall
			if resp != nil {
				mc = telemetry.ModelCall{
					ModelID: resp.ModelID, RequestID: resp.RequestID, Latency: resp.Latency,
					PromptTokens: resp.PromptTokens, CompletionTokens: resp.CompletionTokens,
				}
			}
			telemetry.RecordModelCall(mspan, mc, err)
			mspan.End()

			if err != nil {
				lastErr = err
				break // a transport failure will not be fixed by re-prompting
			}
			lastModel = resp.ModelID

			j, perr := parseJudgment([]byte(resp.Text))
			if perr == nil {
				return j, resp.ModelID, nil
			}
			lastErr = perr
			// Re-prompt once with the validation error appended. Models
			// frequently self-correct a schema slip when shown it.
			user = prompt + "\n\nYour previous reply was rejected: " + perr.Error() + "\nReturn only the required JSON object."
		}
	}
	if lastErr == nil {
		lastErr = llm.ErrNoModel
	}
	return judgment{}, lastModel, lastErr
}

func (f *Firewall) buildPrompt(c Call, pol *policy.Policy, sess *Session, signals []string) string {
	args, _ := json.Marshal(c.Args)
	if len(args) > 2048 {
		args = append(args[:2048], []byte("...[truncated]")...)
	}
	compiled, _ := json.MarshalIndent(pol, "", "  ")

	var history strings.Builder
	for _, sp := range sess.recentTools(10) {
		fmt.Fprintf(&history, "- %s (%s)\n", sp.Name, sp.Status)
	}
	if history.Len() == 0 {
		history.WriteString("- (none)\n")
	}

	intent := pol.DeclaredIntent
	if intent == "" {
		intent = "(no intent declared; permissive baseline in effect)"
	}

	return fmt.Sprintf(`Declared intent: %s

Requested tool: %s
Arguments: %s

Recent tool history:
%s
Budget: $%.2f, spent $%.4f, this call $%.4f

Compiled policy:
%s

Deterministic risk signals already detected: %s`,
		intent, c.Tool, string(args), history.String(),
		c.Budget, c.SessionCost, c.ToolCost, string(compiled),
		orNone(signals))
}

// Commit records a tool call that actually ran, so behavioral and cost history
// reflect executed work rather than attempted work.
func (f *Firewall) Commit(sessionID, tool string, cost float64, dur time.Duration, ok bool) {
	sess := f.sessions.Get(sessionID)
	status := "ok"
	if !ok {
		status = "error"
	}
	sess.RecordSpan(engine.TraceSpan{
		Name: tool, Kind: "tool", Duration: dur, Status: status,
	})
	sess.RecordCost(cost, time.Now())
}

// Forecast returns a session's current cost projection.
func (f *Firewall) Forecast(sessionID string, budget float64) Snapshot {
	sess := f.sessions.Get(sessionID)
	return f.deps.Forecaster.Compute(time.Now(), sess.Cost(), budget, sess.costSamples())
}

// Recent returns the most recent decisions, newest last.
func (f *Firewall) Recent(sessionID string, limit int) []Result {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Result, 0, len(f.recent))
	for _, r := range f.recent {
		if sessionID == "" || r.SessionID == sessionID {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// DropSession clears a disconnected session's state.
func (f *Firewall) DropSession(id string) {
	f.sessions.Drop(id)
	f.deps.Policies.Drop(id)
}

// finish records the decision to telemetry, the audit chain, and the recent
// ring, then returns it. Every decision passes through here, including ALLOW.
func (f *Firewall) finish(ctx context.Context, span telemetry.Span, sess *Session, res Result) Result {
	telemetry.RecordDecision(span, string(res.Decision), res.Stage, res.Reason, res.RuleName, res.RiskScore, res.ModelUsed)

	// Record refused attempts in the behavioral history (but never charge for
	// them — Commit does cost, and only for calls that ran).
	//
	// Without this a tripped behavioral detector is inescapable: blocked calls
	// would never enter the span history, so the pattern that tripped it could
	// never age out of the window and every subsequent call would be refused
	// forever. Recording the attempt is also the more honest history — an agent
	// hammering a tool it keeps being denied is exactly the behavior worth
	// seeing.
	if res.Decision != Allow {
		sess.RecordSpan(engine.TraceSpan{Name: res.Tool, Kind: "tool", Status: "blocked"})
	}

	// Audit every decision, not just the blocks. A trail that records only
	// refusals proves nothing about what was allowed through.
	if f.deps.Ledger != nil {
		e, err := f.deps.Ledger.Append(audit.Event{
			AgentID:   res.SessionID,
			SessionID: res.SessionID,
			Tool:      res.Tool,
			ArgsHash:  audit.HashArgs(nil),
			Decision:  string(res.Decision),
			Reason:    res.Reason,
			ModelUsed: res.ModelUsed,
			Cost:      res.Cost,
		})
		if err != nil {
			// An audit write failure is an error worth shouting about, but it
			// must not block the tool call: losing the record is bad, wedging
			// the agent because of it is worse.
			f.deps.Logger.ErrorContext(ctx, "vigil: audit append failed", slog.String("error", err.Error()))
		} else {
			// Mirror into HydraDB's audit collection, alongside — never instead
			// of — the local hash chain above. The local chain is what makes
			// tampering detectable; this is what makes the trail graph-queryable.
			f.hydraLogAudit(ctx, res, e.PrevHash, e.Hash)
		}
	}

	// Every decision (not just escalated ones) becomes a memory fact, so a
	// future behavioral-drift query has real history to compare against
	// instead of an empty graph on day one.
	f.hydraLogMemory(ctx, res)

	f.mu.Lock()
	f.recent = append(f.recent, res)
	if len(f.recent) > f.deps.RecentSize {
		f.recent = f.recent[len(f.recent)-f.deps.RecentSize:]
	}
	f.mu.Unlock()

	if res.Decision != Allow {
		f.deps.Logger.WarnContext(ctx, "vigil: call not allowed",
			slog.String("session", res.SessionID),
			slog.String("tool", res.Tool),
			slog.String("decision", string(res.Decision)),
			slog.String("stage", res.Stage),
			slog.String("reason", res.Reason),
		)
	}
	return res
}

// recover fires the self-healing engine for a synthesized violation.
func (f *Firewall) recover(ctx context.Context, sess *Session, c Call, v engine.RuleResult) {
	if f.deps.Heal == nil {
		return
	}
	f.deps.Heal.ExecuteRecovery(ctx, sess.AgentContext(c.Budget, c.Tool), v)
}

func maxTier(a, b llm.Tier) llm.Tier {
	if b > a {
		return b
	}
	return a
}

func deterministicReason(signals []string, fallback string) string {
	if len(signals) == 0 {
		return fallback
	}
	return "allowed with deterministic signals noted: " + strings.Join(signals, ", ")
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "(none)"
	}
	return strings.Join(s, ", ")
}

// Behaviour returns a session's observed behavioural profile.
func (f *Firewall) Behaviour(sessionID string) Behaviour {
	return f.sessions.Get(sessionID).Behaviour()
}
