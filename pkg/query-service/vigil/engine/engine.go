package engine

import (
	"context"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// SelfHealingFn is a callback interface for the recovery engine to avoid circular dependencies.
// The ctx carries request-scoped values; agentCtx carries the full agent execution state.
type SelfHealingFn func(ctx context.Context, agentCtx *AgentContext, violation RuleResult)

// GovernanceEngine manages the lifecycle and execution of all registered rule plugins
type GovernanceEngine struct {
	logger *slog.Logger

	// mu guards plugins. Registration happens at construction today but the
	// governance API can add rules at runtime, and EvaluateContext runs on
	// every tool call from many goroutines at once.
	mu      sync.RWMutex
	plugins []DetectorPlugin

	recoveryHook SelfHealingFn
}

// NewGovernanceEngine creates a new engine instance
func NewGovernanceEngine(logger *slog.Logger, hook SelfHealingFn) *GovernanceEngine {
	return &GovernanceEngine{
		logger:       logger,
		plugins:      make([]DetectorPlugin, 0),
		recoveryHook: hook,
	}
}

// RegisterPlugin adds a new detector rule to the engine
func (e *GovernanceEngine) RegisterPlugin(plugin DetectorPlugin) {
	e.mu.Lock()
	e.plugins = append(e.plugins, plugin)
	e.mu.Unlock()
	e.logger.InfoContext(context.Background(), "argus engine: registered plugin",
		slog.String("plugin", plugin.Name()),
	)
}

// EvaluateContext runs the agent trace context through all registered plugins.
// It returns a list of all rule violations found. Each violation is emitted
// as an OTel span event, making it visible in SigNoz Trace Explorer.
func (e *GovernanceEngine) EvaluateContext(ctx context.Context, agentCtx *AgentContext) []RuleResult {
	var violations []RuleResult
	span := trace.SpanFromContext(ctx)

	e.mu.RLock()
	plugins := make([]DetectorPlugin, len(e.plugins))
	copy(plugins, e.plugins)
	e.mu.RUnlock()

	for _, plugin := range plugins {
		result := plugin.Evaluate(agentCtx)
		if result != nil {
			e.logger.WarnContext(ctx, "argus engine: rule triggered",
				slog.String("rule", result.RuleName),
				slog.String("severity", string(result.Severity)),
				slog.String("action", string(result.AutomaticAction)),
				slog.String("reason", result.Reason),
				slog.String("trace_id", agentCtx.TraceID),
			)

			span.AddEvent("vigil.governance.violation", trace.WithAttributes(
				attribute.String("vigil.rule", result.RuleName),
				attribute.String("vigil.severity", string(result.Severity)),
				attribute.String("vigil.action", string(result.AutomaticAction)),
				attribute.String("vigil.reason", result.Reason),
				attribute.String("vigil.trace_id", agentCtx.TraceID),
				attribute.String("vigil.project", agentCtx.ProjectName),
			))

			violations = append(violations, *result)

			if e.recoveryHook != nil {
				e.recoveryHook(ctx, agentCtx, *result)
			}
		}
	}

	return violations
}

// Plugins returns the names of every registered detector, in evaluation order.
//
// The governance API reports exactly this, rather than a hardcoded list: a rule
// that is not registered cannot fire, and advertising it as active would be a
// lie the dashboard repeats.
func (e *GovernanceEngine) Plugins() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.plugins))
	for _, p := range e.plugins {
		names = append(names, p.Name())
	}
	return names
}
