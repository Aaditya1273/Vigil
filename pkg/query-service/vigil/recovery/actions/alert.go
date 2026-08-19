package actions

import (
	"context"
	"fmt"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/recovery"
)

// Notifier delivers a governance alert somewhere a human will see it.
type Notifier func(ctx context.Context, agentCtx *engine.AgentContext, violation engine.RuleResult) error

// AlertAction handles engine.ActionAlert.
//
// Before this existed nothing was registered for ActionAlert, so the three
// detectors that emit it (agent stuck, tool timeout, prompt recursion) fell
// through to the engine's "no action registered" warning and produced no
// notification at all.
//
// Unlike the eight pre-existing actions, this one does real work and reports
// real failure. An action that unconditionally returns Success:true tells you
// nothing about whether anyone was actually alerted.
type AlertAction struct {
	notify Notifier
}

func NewAlertAction(notify Notifier) *AlertAction {
	return &AlertAction{notify: notify}
}

func (a *AlertAction) Name() string { return "Alert" }

func (a *AlertAction) ActionType() engine.AutomaticAction { return engine.ActionAlert }

func (a *AlertAction) Execute(ctx context.Context, agentCtx *engine.AgentContext, violation engine.RuleResult) *recovery.RecoveryResult {
	if a.notify == nil {
		return &recovery.RecoveryResult{
			Success: false,
			Message: "no notifier configured; alert was not delivered",
		}
	}
	if err := a.notify(ctx, agentCtx, violation); err != nil {
		return &recovery.RecoveryResult{
			Success: false,
			Message: fmt.Sprintf("alert delivery failed: %v", err),
		}
	}
	return &recovery.RecoveryResult{
		Success: true,
		Message: fmt.Sprintf("alerted on %s (%s)", violation.RuleName, violation.Severity),
	}
}
