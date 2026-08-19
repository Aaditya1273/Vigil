package recovery

import (
	"context"
	"log/slog"
	"sync"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("vigil.recovery")

// SelfHealingEngine orchestrates the automated recovery of agents
type SelfHealingEngine struct {
	logger *slog.Logger

	// mu guards actions. ExecuteRecovery is reached from the governance hook on
	// the tool-call hot path, i.e. from many goroutines at once.
	mu      sync.RWMutex
	actions map[engine.AutomaticAction]RecoveryAction
}

// NewSelfHealingEngine creates a new recovery engine
func NewSelfHealingEngine(logger *slog.Logger) *SelfHealingEngine {
	return &SelfHealingEngine{
		logger:  logger,
		actions: make(map[engine.AutomaticAction]RecoveryAction),
	}
}

// RegisterAction maps a governance action type to a concrete recovery strategy
func (s *SelfHealingEngine) RegisterAction(action RecoveryAction) {
	s.mu.Lock()
	s.actions[action.ActionType()] = action
	s.mu.Unlock()
	s.logger.InfoContext(context.Background(), "vigil recovery: registered action",
		slog.String("action", action.Name()),
		slog.String("type", string(action.ActionType())),
	)
}

// ExecuteRecovery runs the mapped recovery strategy and emits a full lifecycle trace
func (s *SelfHealingEngine) ExecuteRecovery(ctx context.Context, agentCtx *engine.AgentContext, violation engine.RuleResult) *RecoveryResult {
	s.mu.RLock()
	action, exists := s.actions[violation.AutomaticAction]
	s.mu.RUnlock()
	if !exists {
		s.logger.WarnContext(ctx, "vigil recovery: no action registered",
			slog.String("action_type", string(violation.AutomaticAction)),
			slog.String("trace_id", agentCtx.TraceID),
		)
		return nil
	}

	// Start OpenTelemetry span to trace the automated decision and recovery
	ctx, span := tracer.Start(ctx, "Automated Recovery: "+action.Name(), trace.WithAttributes(
		attribute.String("vigil.problem.rule", violation.RuleName),
		attribute.String("vigil.problem.reason", violation.Reason),
		attribute.String("vigil.problem.severity", string(violation.Severity)),
		attribute.String("vigil.decision.action", string(violation.AutomaticAction)),
		attribute.String("vigil.agent.trace_id", agentCtx.TraceID),
		attribute.String("vigil.agent.project", agentCtx.ProjectName),
	))
	defer span.End()

	s.logger.InfoContext(ctx, "vigil recovery: executing",
		slog.String("action", action.Name()),
		slog.String("trace_id", agentCtx.TraceID),
	)
	result := action.Execute(ctx, agentCtx, violation)
	if result == nil {
		// Guard here rather than in each action: every field below dereferences
		// result, so one action returning nil would panic the tool-call path.
		result = &RecoveryResult{Success: false, Message: "recovery action returned no result"}
	}

	// Record outcome
	span.SetAttributes(
		attribute.Bool("vigil.outcome.success", result.Success),
		attribute.String("vigil.outcome.message", result.Message),
	)

	if !result.Success {
		span.RecordError(nil, trace.WithAttributes(attribute.String("error.message", result.Message)))
		s.logger.ErrorContext(ctx, "vigil recovery: action failed",
			slog.String("action", action.Name()),
			slog.String("message", result.Message),
		)
	}

	return result
}
