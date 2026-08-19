package firewall_test

import (
	"context"
	"os"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

func withCompromisedEnv(t *testing.T, value string) {
	t.Helper()
	old, had := os.LookupEnv("VIGIL_COMPROMISED_PACKAGES")
	os.Setenv("VIGIL_COMPROMISED_PACKAGES", value)
	t.Cleanup(func() {
		if had {
			os.Setenv("VIGIL_COMPROMISED_PACKAGES", old)
		} else {
			os.Unsetenv("VIGIL_COMPROMISED_PACKAGES")
		}
	})
}

// TestCompromisedPackageBlocksImmediately is the incident-response path: a
// package on the denylist is blocked without waiting on the graph-based
// typosquat heuristic — the whole point of a confirmed-compromised list is
// that the answer is already known.
func TestCompromisedPackageBlocksImmediately(t *testing.T) {
	withCompromisedEnv(t, "evil-tanstack-plugin")

	store := policy.NewStore()
	netPol := &policy.Policy{SessionID: "s-incident", DeclaredIntent: "install packages", NetworkAccess: true, BudgetUSD: 2}
	netPol.Normalize()
	store.Set(netPol)

	f := firewall.New(firewall.Deps{
		Logger:      newTestLogger(t),
		Policies:    store,
		Compromised: firewall.NewCompromisedList(),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-incident", Tool: "run_command",
		Args: map[string]any{"command": "npm install evil-tanstack-plugin"}, ToolCost: 0.001, Budget: 2,
	})

	if res.Decision != firewall.Block {
		t.Fatalf("Expected BLOCK for a compromised package, got %s (%s)", res.Decision, res.Reason)
	}
	if res.Stage != firewall.StageCodeGraph {
		t.Fatalf("Expected stage %q, got %q", firewall.StageCodeGraph, res.Stage)
	}
}

// TestCompromisedPackageBlockWithoutHydraStillBlocks confirms the block
// itself never depends on HydraDB being reachable — only the attached
// exposure report does. A confirmed-compromised package is blocked whether
// or not the graph layer is configured.
func TestCompromisedPackageBlockWithoutHydraStillBlocks(t *testing.T) {
	withCompromisedEnv(t, "evil-tanstack-plugin@1.2.3")

	store := policy.NewStore()
	netPol := &policy.Policy{SessionID: "s-no-hydra", DeclaredIntent: "install packages", NetworkAccess: true, BudgetUSD: 2}
	netPol.Normalize()
	store.Set(netPol)

	f := firewall.New(firewall.Deps{
		Logger:      newTestLogger(t),
		Policies:    store,
		Compromised: firewall.NewCompromisedList(),
		// Hydra deliberately nil.
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-no-hydra", Tool: "run_command",
		Args: map[string]any{"command": "npm install evil-tanstack-plugin@1.2.3"}, ToolCost: 0.001, Budget: 2,
	})

	if res.Decision != firewall.Block {
		t.Fatalf("Expected BLOCK even with no HydraDB configured, got %s (%s)", res.Decision, res.Reason)
	}
	if res.SupplyChain != nil {
		t.Error("Expected no SupplyChain report when HydraDB is unconfigured, since there's nothing to query")
	}
}

// TestUnlistedPackageFallsThroughToTyposquatCheck confirms an empty/no-match
// compromised list doesn't short-circuit the existing graph-based check.
func TestUnlistedPackageFallsThroughToTyposquatCheck(t *testing.T) {
	withCompromisedEnv(t, "")

	store := policy.NewStore()
	netPol := &policy.Policy{SessionID: "s-clean", DeclaredIntent: "install packages", NetworkAccess: true, BudgetUSD: 2}
	netPol.Normalize()
	store.Set(netPol)

	f := firewall.New(firewall.Deps{
		Logger:      newTestLogger(t),
		Policies:    store,
		Compromised: firewall.NewCompromisedList(),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-clean", Tool: "run_command",
		Args: map[string]any{"command": "npm install left-pad"}, ToolCost: 0.001, Budget: 2,
	})

	// No HydraDB configured either, so this should ALLOW (fails open past the
	// unconfigured graph check) rather than BLOCK — proves the compromised-list
	// check isn't accidentally blocking everything.
	if res.Decision != firewall.Allow {
		t.Fatalf("Expected ALLOW for an unlisted package with no graph configured, got %s (%s)", res.Decision, res.Reason)
	}
}
