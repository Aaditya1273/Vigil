package firewall

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
)

// hydraTimeout bounds every graph query the firewall makes, and (for the
// fire-and-forget ingest calls) the queue wait for an ingestion rate-limit
// token on top of the HTTP round trip. HydraDB's own /query endpoint measured
// 150ms-2.5s single-call; under a demo-level burst of concurrent ingests, the
// token wait alone can eat a couple of seconds of a 4s budget, which was
// observed to time out live — this bound must still be bounded, an
// unreachable HydraDB must degrade to the existing deterministic/Featherless
// path rather than hang the call, but 4s was too tight for real burst
// behavior, not just single-call latency.
const hydraTimeout = 8 * time.Second

// packageInstallRe pulls a package name out of a pip/npm/yarn install
// command, so a supply-chain check has something to look up. Best-effort:
// this is heuristic string matching on a shell command, same caveat as
// policy.networkVerbs — it raises scrutiny, it is not a parser.
var packageInstallRe = regexp.MustCompile(`(?:pip install|npm install|npm i |yarn add)\s+(?:-\S+\s+)*([a-zA-Z0-9_.@/\-]+)`)

// blastRadiusTarget returns the package name a call is about to install, or
// "" if this call isn't install-shaped. Only run_command is checked because
// it's the only exec-capable tool this codebase has (policy.toolCategories);
// a dedicated npm_install/pip_install tool would be added here the same way.
func blastRadiusTarget(c Call) string {
	if c.Tool != "run_command" {
		return ""
	}
	cmd, _ := c.Args["command"].(string)
	return extractPackageName(cmd)
}

func extractPackageName(cmd string) string {
	m := packageInstallRe.FindStringSubmatch(strings.ToLower(cmd))
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// graphFinding is what a HydraDB consult contributed to one decision, kept
// alongside the deterministic signals so the audit record and the dashboard
// can show the graph reasoning, not just its conclusion.
type graphFinding struct {
	collection string
	query      string
	paths      []string
	latencyMS  float64
	resolved   bool // true if the graph itself answered the question
}

func (g graphFinding) empty() bool { return g.query == "" }

// hydraQuery is the single choke point every graph consult goes through: bound
// the timeout, and on any failure (unconfigured, network error, timeout)
// degrade to "the graph had nothing to say" rather than propagating an error
// up into the decision path. HydraDB being unreachable must never be the
// reason a call is blocked or allowed by accident.
func (f *Firewall) hydraQuery(ctx context.Context, collection, queryType, query string) (hydra.QueryResult, bool) {
	if !f.deps.Hydra.Configured() {
		return hydra.QueryResult{}, false
	}
	qctx, cancel := context.WithTimeout(ctx, hydraTimeout)
	defer cancel()
	res, err := f.deps.Hydra.Query(qctx, collection, queryType, query)
	if err != nil {
		f.deps.Logger.WarnContext(ctx, "vigil: hydra query failed, falling through",
			"collection", collection, "error", err.Error())
		return hydra.QueryResult{}, false
	}
	return res, true
}

// hydraIntentCheck asks the enterprise knowledge graph whether policy covers
// this call, before the deterministic layer's UNCERTAIN verdict is allowed to
// escalate to Featherless. This is the graph-first replacement for "ask a
// model when the allowlist doesn't cover a tool": ask the graph first, ask a
// model only if the graph itself has nothing.
func (f *Firewall) hydraIntentCheck(ctx context.Context, c Call) (verdict string, finding graphFinding) {
	q := fmt.Sprintf("What policy applies to tool %q for this session, and is it permitted?", c.Tool)
	res, ok := f.hydraQuery(ctx, hydra.CollectionEnterprise, "knowledge", q)
	finding = graphFinding{collection: hydra.CollectionEnterprise, query: q}
	if !ok {
		return "", finding
	}
	finding.paths = res.EntityPaths()
	finding.latencyMS = res.LatencyMS
	if !res.HasGraphSignal() {
		return "", finding
	}
	finding.resolved = true
	// The graph found a policy relationship for this tool. Whether it reads as
	// permitting or forbidding is judged from the extracted predicate text,
	// deliberately conservative: an ambiguous predicate stays UNCERTAIN rather
	// than being read as permission, same fail-closed posture as everywhere
	// else in this pipeline.
	for _, cr := range res.GraphContext.ChunkRelations {
		for _, t := range cr.Triplets {
			pred := strings.ToLower(t.Relation.Predicate)
			// Real bug found via a live HydraDB query: the extracted
			// canonical_predicate for a denial is literally "denies"
			// (present tense) — not a substring of "denied", so this check
			// silently never matched real graph output. Confirmed by
			// dumping an actual triplet: "policy no-pii-exfil --[denies]-->
			// customer personal data export".
			if strings.Contains(pred, "deny") || strings.Contains(pred, "denies") || strings.Contains(pred, "denied") || strings.Contains(pred, "forbid") || strings.Contains(pred, "block") {
				return "deny", finding
			}
			if strings.Contains(pred, "applies to") || strings.Contains(pred, "permit") || strings.Contains(pred, "allow") {
				return "allow", finding
			}
		}
	}
	return "uncertain", finding
}

// hydraBlastRadius asks the code_graph collection about a package's
// transitive reverse-dependency exposure and maintainer graph before an
// install/exec call proceeds. This runs unconditionally for install-shaped
// commands (not gated behind an escalation tier) — it is exactly the
// category HydraDB's own product brief names as the case that must not be
// resolvable without a graph query.
func (f *Firewall) hydraBlastRadius(ctx context.Context, pkg string) (highRisk bool, finding graphFinding) {
	q := "What is the transitive reverse dependency closure and maintainer graph for " + pkg + "? Is it a typosquat of a popular package?"
	res, ok := f.hydraQuery(ctx, hydra.CollectionCodeGraph, "knowledge", q)
	finding = graphFinding{collection: hydra.CollectionCodeGraph, query: q}
	if !ok {
		return false, finding
	}
	finding.paths = res.EntityPaths()
	finding.latencyMS = res.LatencyMS
	finding.resolved = res.HasGraphSignal()
	for _, cr := range res.GraphContext.ChunkRelations {
		for _, t := range cr.Triplets {
			pred := strings.ToLower(t.Relation.Predicate)
			if strings.Contains(pred, "typosquat") {
				return true, finding
			}
		}
	}
	return false, finding
}

// hydraBehaviorCheck asks agent_memory whether this session's recent tool
// pattern has been seen before, once the deterministic behavioral engine has
// already raised a signal. It does not decide on its own — it is context the
// judge (or a human) uses to tell "this agent always does this" from
// "this agent has never done this before".
func (f *Firewall) hydraBehaviorCheck(ctx context.Context, c Call, signals []string, agentCtx *engine.AgentContext) graphFinding {
	pattern := toolCallSummary(agentCtx)
	if pattern == "" {
		pattern = strings.Join(signals, "+")
	}
	// "Is this pattern anomalous... or does it match normal behavioral DNA"
	// rather than "has this pattern been seen before" — found live that the
	// weaker phrasing let generic decision-log noise (agent_memory holds
	// every firewall decision ever logged, not just behavioral baselines)
	// outrank an ingested behavioral-DNA baseline that literally uses the
	// word "anomalous". Echoing the vocabulary a baseline statement would
	// naturally use is what surfaced it reliably.
	q := fmt.Sprintf("Is this pattern anomalous for this agent, or does it match normal behavioral DNA: %s?", pattern)
	res, ok := f.hydraQuery(ctx, hydra.CollectionMemory, "memory", q)
	finding := graphFinding{collection: hydra.CollectionMemory, query: q}
	if !ok {
		return finding
	}
	finding.paths = res.EntityPaths()
	finding.latencyMS = res.LatencyMS
	finding.resolved = res.HasGraphSignal()
	return finding
}

// toolCallSummary turns the raw span ring into a compact "tool xN" pattern
// description — e.g. "search_code x19, network_request x3" — the same shape
// a human would describe an anomaly in, and concrete enough for agent_memory
// to match against a previously-ingested behavioral-DNA baseline. Order is
// preserved (first-seen tool first) so "read x1, search x1, test x1" reads
// as a sequence, not just a bag of counts.
func toolCallSummary(agentCtx *engine.AgentContext) string {
	if agentCtx == nil {
		return ""
	}
	counts := map[string]int{}
	var order []string
	for _, s := range agentCtx.Spans {
		if s.Kind != "tool" {
			continue
		}
		if counts[s.Name] == 0 {
			order = append(order, s.Name)
		}
		counts[s.Name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, fmt.Sprintf("%s x%d", name, counts[name]))
	}
	return strings.Join(parts, ", ")
}

// hydraLogMemory records this decision into agent_memory, fire-and-forget, so
// future behavioral-drift queries have something to compare against. Ingest
// is asynchronous server-side already; this call only waits for HydraDB to
// accept the write, not to index it — indexing happens on HydraDB's own time,
// same as any other ingest in this codebase.
func (f *Firewall) hydraLogMemory(ctx context.Context, res Result) {
	if !f.deps.Hydra.Configured() {
		return
	}
	text := fmt.Sprintf("Session %s called %s -> %s (%s): %s", res.SessionID, res.Tool, res.Decision, res.Stage, res.Reason)
	go func() {
		ictx, cancel := context.WithTimeout(context.Background(), hydraTimeout)
		defer cancel()
		if _, err := f.deps.Hydra.IngestMemory(ictx, hydra.CollectionMemory,
			fmt.Sprintf("decision-%s-%d", res.SessionID, res.At.UnixNano()), text); err != nil {
			f.deps.Logger.WarnContext(ctx, "vigil: hydra memory ingest failed", "error", err.Error())
		}
	}()
}

// hydraLogAudit mirrors a decision into HydraDB's audit collection, alongside
// (never instead of) the local SHA-256 hash chain in audit/ledger.go. The
// local chain is the tamper-evidence guarantee; this is what makes the audit
// trail itself graph-queryable — "show me every BLOCK that traces back to
// this package" is a graph question, not a linear scan.
func (f *Firewall) hydraLogAudit(ctx context.Context, res Result, prevHash, hash string) {
	if !f.deps.Hydra.Configured() {
		return
	}
	text := fmt.Sprintf(
		"Decision %s for session %s calling %s at stage %s: %s. prev_hash=%s hash=%s",
		res.Decision, res.SessionID, res.Tool, res.Stage, res.Reason, prevHash, hash,
	)
	go func() {
		ictx, cancel := context.WithTimeout(context.Background(), hydraTimeout)
		defer cancel()
		if _, err := f.deps.Hydra.IngestMemory(ictx, hydra.CollectionAudit,
			fmt.Sprintf("audit-%s-%d", res.SessionID, res.At.UnixNano()), text); err != nil {
			f.deps.Logger.WarnContext(ctx, "vigil: hydra audit mirror failed", "error", err.Error())
		}
	}()
}
