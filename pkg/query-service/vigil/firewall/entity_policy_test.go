package firewall_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

// TestEntityPolicyBlocksShareToResolvedCustomer is the task's own scenario:
// an agent tries to share a file with "Sam". The deterministic policy layer
// has no idea who "Sam" is — the whole point of entity resolution is that a
// bare display name means nothing on its own. The enterprise graph resolves
// it through a real SAME_AS chain to a Customer, and policy denies sharing
// with that entity type.
func TestEntityPolicyBlocksShareToResolvedCustomer(t *testing.T) {
	stub := &hydraStub{reply: []struct {
		contains string
		triplets []map[string]any
	}{
		{contains: "", triplets: []map[string]any{
			{
				"source":   map[string]any{"entity_id": "e1", "name": "sam", "type": "PERSON"},
				"relation": map[string]any{"canonical_predicate": "also known as", "context": "stub"},
				"target":   map[string]any{"entity_id": "e2", "name": "soham ratnaparkhi", "type": "PERSON"},
			},
			{
				"source":   map[string]any{"entity_id": "e3", "name": "sam", "type": "PERSON"},
				"relation": map[string]any{"canonical_predicate": "resolves to entity type customer, denied by policy", "context": "stub"},
				"target":   map[string]any{"entity_id": "e4", "name": "customer", "type": "CONCEPT"},
			},
		}},
	}}
	srv := stub.server(t)
	defer srv.Close()

	store := policy.NewStore()
	p := &policy.Policy{SessionID: "s-share", DeclaredIntent: "file operations", NetworkAccess: true, BudgetUSD: 2}
	p.Normalize()
	store.Set(p)

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Hydra:    hydra.New(srv.URL, "test-key", "test-db", newTestLogger(t)),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-share", Tool: "run_command",
		Args: map[string]any{"command": "share the export.csv report with Sam"}, ToolCost: 0.001, Budget: 2,
	})

	if res.Decision != firewall.Block {
		t.Fatalf("Expected BLOCK for a share to a resolved Customer, got %s (%s)", res.Decision, res.Reason)
	}
	if res.Stage != firewall.StageEntityPolicy {
		t.Fatalf("Expected stage %q, got %q", firewall.StageEntityPolicy, res.Stage)
	}
	if res.EntityPolicy == nil {
		t.Fatal("Expected an EntityPolicyReport attached to the block")
	}
	if res.EntityPolicy.EntityType != "customer" {
		t.Errorf("Expected entity type 'customer', got %q", res.EntityPolicy.EntityType)
	}
	if len(res.EntityPolicy.ResolvedAliases) == 0 {
		t.Error("Expected at least one resolved alias in the report")
	}
}

// TestEntityPolicyAllowsShareToUnresolvedEmployee confirms the check is
// real adjudication, not a rubber stamp on every share-shaped call: a name
// the graph resolves to a non-protected type (or can't characterize at all)
// must not block.
func TestEntityPolicyAllowsShareToUnresolvedEmployee(t *testing.T) {
	stub := &hydraStub{} // no triplets — graph has nothing on this name
	srv := stub.server(t)
	defer srv.Close()

	store := policy.NewStore()
	p := &policy.Policy{SessionID: "s-ok-share", DeclaredIntent: "file operations", NetworkAccess: true, BudgetUSD: 2}
	p.Normalize()
	store.Set(p)

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Hydra:    hydra.New(srv.URL, "test-key", "test-db", newTestLogger(t)),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-ok-share", Tool: "run_command",
		Args: map[string]any{"command": "share the notes.txt file with Priya"}, ToolCost: 0.001, Budget: 2,
	})

	if res.Decision != firewall.Allow {
		t.Fatalf("Expected ALLOW when the graph has no protected-entity signal, got %s (%s)", res.Decision, res.Reason)
	}
}

// TestEntityPolicyIgnoresCallsWithNoShareTarget confirms an ordinary call
// with no recipient never triggers a graph lookup at all.
func TestEntityPolicyIgnoresCallsWithNoShareTarget(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"success":true,"data":{"chunks":[],"graph_context":{"chunk_relations":[],"query_paths":[]}}}`))
	}))
	defer srv.Close()

	store := policy.NewStore()
	p := &policy.Policy{SessionID: "s-no-share", DeclaredIntent: "reading files", AllowedTools: []string{"read_file"}, BudgetUSD: 2}
	p.Normalize()
	store.Set(p)

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Hydra:    hydra.New(srv.URL, "test-key", "test-db", newTestLogger(t)),
	})

	f.Check(context.Background(), firewall.Call{
		SessionID: "s-no-share", Tool: "read_file",
		Args: map[string]any{"path": "README.md"}, ToolCost: 0.001, Budget: 2,
	})

	if calls != 0 {
		t.Errorf("Expected zero HydraDB calls for a call with no share target, got %d", calls)
	}
}
