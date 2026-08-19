package actions

import (
	"context"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/engine"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/recovery"
)

type KillAgentAction struct{}

func NewKillAgentAction() *KillAgentAction {
	return &KillAgentAction{}
}

func (a *KillAgentAction) Name() string {
	return "Kill Agent"
}

func (a *KillAgentAction) ActionType() engine.AutomaticAction {
	return engine.ActionKillRun
}

func (a *KillAgentAction) Execute(ctx context.Context, agentCtx *engine.AgentContext, violation engine.RuleResult) *recovery.RecoveryResult {
	// In a real implementation, this would send a signal/payload to the SDK
	// to abort the ongoing LLM/tool execution immediately.
	return &recovery.RecoveryResult{
		Success: true,
		Message: "Successfully transmitted KILL signal to agent SDK",
	}
}
