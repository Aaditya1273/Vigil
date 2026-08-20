package firewall_test

import (
	"context"
	"os"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

// TestLiveEntityPolicy is the task's own scenario run against the real
// HydraDB graph, not a stub: scripts/ingest_enterprise.py's simulated
// Northwind Signal corpus has Jordan Blake as its one real Customer contact
// and everyone else (including Sam/Soham Ratnaparkhi) as an Employee.
// Sharing with a resolved Customer must block; sharing with a resolved
// Employee must not.
//
// Skipped unless a key is present, same gating as llm/live_test.go, so the
// default `go test ./...` stays offline and credential-free.
//
//	VIGIL_HYDRADB_API_KEY=... VIGIL_HYDRADB_DATABASE=... go test -run TestLiveEntityPolicy -v ./pkg/query-service/vigil/firewall/
func TestLiveEntityPolicy(t *testing.T) {
	key := os.Getenv("VIGIL_HYDRADB_API_KEY")
	if key == "" {
		t.Skip("skipping: VIGIL_HYDRADB_API_KEY unset")
	}
	db := os.Getenv("VIGIL_HYDRADB_DATABASE")
	base := os.Getenv("VIGIL_HYDRADB_BASE_URL")
	if base == "" {
		base = "https://api.hydradb.com"
	}
	h := hydra.New(base, key, db, newTestLogger(t))

	newFirewall := func(sessionID string) *firewall.Firewall {
		store := policy.NewStore()
		p := &policy.Policy{SessionID: sessionID, DeclaredIntent: "file operations", NetworkAccess: true, BudgetUSD: 2}
		p.Normalize()
		store.Set(p)
		return firewall.New(firewall.Deps{Logger: newTestLogger(t), Policies: store, Hydra: h})
	}

	t.Run("blocks share to the real resolved Customer", func(t *testing.T) {
		f := newFirewall("live-jordan")
		res := f.Check(context.Background(), firewall.Call{
			SessionID: "live-jordan", Tool: "run_command",
			Args: map[string]any{"command": "share the export.csv report with Jordan"}, ToolCost: 0.001, Budget: 2,
		})
		if res.Decision != firewall.Block {
			t.Fatalf("expected BLOCK sharing with Jordan (real Customer), got %s (%s)", res.Decision, res.Reason)
		}
		if res.EntityPolicy == nil || res.EntityPolicy.EntityType != "customer" {
			t.Fatalf("expected entity_type=customer in the report, got %+v", res.EntityPolicy)
		}
		t.Logf("blocked: %s — aliases: %v", res.Reason, res.EntityPolicy.ResolvedAliases)
	})

	t.Run("allows share to the real resolved Employee", func(t *testing.T) {
		f := newFirewall("live-sam")
		res := f.Check(context.Background(), firewall.Call{
			SessionID: "live-sam", Tool: "run_command",
			Args: map[string]any{"command": "share the export.csv report with Sam"}, ToolCost: 0.001, Budget: 2,
		})
		if res.Decision != firewall.Allow {
			t.Fatalf("expected ALLOW sharing with Sam (real Employee), got %s (%s)", res.Decision, res.Reason)
		}
	})
}
