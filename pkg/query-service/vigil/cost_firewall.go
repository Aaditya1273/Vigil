package vigil

import (
	"context"
	"fmt"
	"sync"
)

// CostFirewall tracks fleet-wide spend against a ceiling.
//
// This is deliberately a *fleet* aggregate — it answers "what is this
// deployment burning in total". Per-session enforcement lives on the MCP
// session's own budget; do not use CurrentBurn to attribute cost to an
// individual agent, which is what the pre-2.0 code did and why every agent
// showed the same figure.
type CostFirewall struct {
	mu          sync.Mutex
	BudgetLimit float64
	currentBurn float64
}

func NewCostFirewall(budget float64) *CostFirewall {
	return &CostFirewall{BudgetLimit: budget}
}

// Add applies a charge and returns the new total burn.
func (cf *CostFirewall) Add(cost float64) float64 {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	cf.currentBurn += cost
	return cf.currentBurn
}

// Burn returns the current total burn.
func (cf *CostFirewall) Burn() float64 {
	cf.mu.Lock()
	defer cf.mu.Unlock()
	return cf.currentBurn
}

// EvaluateRun applies a charge and reports whether the fleet ceiling still holds.
func (cf *CostFirewall) EvaluateRun(ctx context.Context, agentID string, cost float64) (bool, error) {
	burn := cf.Add(cost)
	if burn > cf.BudgetLimit {
		return false, fmt.Errorf("agent %s tripped circuit breaker: budget exceeded (burn: %.2f, limit: %.2f)", agentID, burn, cf.BudgetLimit)
	}
	return true, nil
}
