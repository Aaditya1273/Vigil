package firewall_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/firewall"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/llm"
	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

// hydraStub stands up a fake HydraDB API shaped exactly like the real one
// (verified empirically against api.hydradb.com — see hydra/hydra_test.go),
// so these tests exercise the real request/response parsing without a live
// credential. What it returns per query is set per test.
type hydraStub struct {
	// reply maps a substring of the incoming query to a canned graph_context.
	// The first match wins; this is enough to drive each test's one query.
	reply []struct {
		contains string
		triplets []map[string]any
	}
	queries []string // every query string this stub actually received
}

func (h *hydraStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/databases" && r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{
				"success": true, "data": map[string]any{"databases": []string{"test-db"}},
			})
		case r.URL.Path == "/context/ingest":
			json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"results": []map[string]any{{"id": "src-1", "status": "queued"}},
				},
			})
		case r.URL.Path == "/query" && r.Method == http.MethodPost:
			var body struct {
				Query string `json:"query"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			h.queries = append(h.queries, body.Query)

			var triplets []map[string]any
			for _, c := range h.reply {
				if c.contains == "" || contains(body.Query, c.contains) {
					triplets = c.triplets
					break
				}
			}
			chunkRelations := []map[string]any{}
			if len(triplets) > 0 {
				chunkRelations = []map[string]any{{"triplets": triplets, "combined_context": "stub"}}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"chunks":        []map[string]any{},
					"graph_context": map[string]any{"chunk_relations": chunkRelations, "query_paths": []any{}},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func triplet(predicate string) map[string]any {
	return map[string]any{
		"source":   map[string]any{"entity_id": "e1", "name": "subject", "type": "CONCEPT"},
		"relation": map[string]any{"canonical_predicate": predicate, "context": "stub context"},
		"target":   map[string]any{"entity_id": "e2", "name": "object", "type": "CONCEPT"},
	}
}

// TestBlastRadiusBlocksWithoutTheModel is the failure-mode HydraDB's own
// product brief calls out explicitly: an install command must be checked
// against the code_graph collection, and a typosquat finding there must
// block the call *before* any model is ever consulted — the graph decides,
// not an LLM guessing from the package name alone.
func TestBlastRadiusBlocksWithoutTheModel(t *testing.T) {
	stub := &hydraStub{reply: []struct {
		contains string
		triplets []map[string]any
	}{
		{contains: "reqeusts", triplets: []map[string]any{triplet("typosquat")}},
	}}
	srv := stub.server(t)
	defer srv.Close()

	modelStub := &stubProvider{onCall: func() { t.Fatal("blast radius must block on the graph finding before a model is ever consulted") }}
	store := policy.NewStore()
	// A policy that actually permits network/exec: the point of this test is
	// the graph check specifically, not the deterministic "no network" policy
	// gate that would otherwise block pip install before step 2 is reached.
	netPol := &policy.Policy{SessionID: "s-blast", DeclaredIntent: "install packages", NetworkAccess: true, BudgetUSD: 2}
	netPol.Normalize()
	store.Set(netPol)

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Router:   llm.NewRouter(newTestLogger(t), modelStub),
		Hydra:    hydra.New(srv.URL, "test-key", "test-db", newTestLogger(t)),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-blast", Tool: "run_command",
		Args: map[string]any{"command": "pip install reqeusts"}, ToolCost: 0.001, Budget: 2,
	})

	if res.Decision != firewall.Block {
		t.Fatalf("Expected BLOCK from the code_graph finding, got %s (%s)", res.Decision, res.Reason)
	}
	if res.Stage != firewall.StageCodeGraph {
		t.Fatalf("Expected stage %q, got %q", firewall.StageCodeGraph, res.Stage)
	}
	if !res.GraphQueried {
		t.Fatal("Expected GraphQueried=true — this is the field the acceptance criteria checks for")
	}
	if len(res.GraphPaths) == 0 {
		t.Fatal("Expected at least one graph_path in the decision, got none")
	}
	if len(stub.queries) != 1 {
		t.Fatalf("Expected exactly one query to the code_graph collection, got %d", len(stub.queries))
	}
}

// TestBlastRadiusAllowsAnOrdinaryPackage confirms the graph consult is real
// adjudication, not a rubber stamp: a package the graph says nothing bad
// about must be allowed to proceed.
func TestBlastRadiusAllowsAnOrdinaryPackage(t *testing.T) {
	stub := &hydraStub{} // no triplets for any query
	srv := stub.server(t)
	defer srv.Close()

	store := policy.NewStore()
	netPol := &policy.Policy{SessionID: "s-ok", DeclaredIntent: "install packages", NetworkAccess: true, BudgetUSD: 2}
	netPol.Normalize()
	store.Set(netPol)

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Hydra:    hydra.New(srv.URL, "test-key", "test-db", newTestLogger(t)),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-ok", Tool: "run_command",
		Args: map[string]any{"command": "pip install requests"}, ToolCost: 0.001, Budget: 2,
	})
	if res.Decision != firewall.Allow {
		t.Fatalf("Expected ALLOW for an unflagged package, got %s (%s)", res.Decision, res.Reason)
	}
	if len(stub.queries) != 1 {
		t.Fatalf("Expected the graph to still be consulted once, got %d queries", len(stub.queries))
	}
}

// TestGraphResolvesIntentWithoutTheModel is the enterprise/intent path: an
// UNCERTAIN policy verdict must ask HydraDB before it's allowed to reach
// Featherless. A graph answer that reads as permission resolves the call
// without ever invoking the model.
func TestGraphResolvesIntentWithoutTheModel(t *testing.T) {
	stub := &hydraStub{reply: []struct {
		contains string
		triplets []map[string]any
	}{
		{contains: "", triplets: []map[string]any{triplet("applies to")}},
	}}
	srv := stub.server(t)
	defer srv.Close()

	modelStub := &stubProvider{onCall: func() { t.Fatal("an intent question the graph already answered must not reach a model") }}
	store := policy.NewStore()
	// No AllowedTools list restricting "vigil_agent_dna" -> Evaluate returns
	// UNCERTAIN only when AllowedTools is non-empty and doesn't cover the tool.
	p := &policy.Policy{SessionID: "s-intent", DeclaredIntent: "ops", AllowedTools: []string{"read_file"}, BudgetUSD: 2}
	p.Normalize()
	store.Set(p)

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Router:   llm.NewRouter(newTestLogger(t), modelStub),
		Hydra:    hydra.New(srv.URL, "test-key", "test-db", newTestLogger(t)),
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-intent", Tool: "vigil_agent_dna",
		Args: map[string]any{}, ToolCost: 0.001, Budget: 2,
	})

	if res.Decision != firewall.Allow {
		t.Fatalf("Expected ALLOW once the enterprise graph resolved the intent question, got %s (%s)", res.Decision, res.Reason)
	}
	if !res.GraphQueried {
		t.Fatal("Expected GraphQueried=true")
	}
	if modelStub.calls != 0 {
		t.Fatalf("Expected the model to never be called, got %d calls", modelStub.calls)
	}
}

// TestHydraUnreachableFallsBackToExistingBehavior proves a HydraDB outage
// degrades to the pre-existing deterministic/Featherless pipeline rather than
// wedging every call or silently allowing/blocking by accident.
func TestHydraUnreachableFallsBackToExistingBehavior(t *testing.T) {
	unreachable := hydra.New("http://127.0.0.1:1", "test-key", "test-db", newTestLogger(t))
	store := policy.NewStore()
	store.Set(fixTestsPolicy("s-down"))

	f := firewall.New(firewall.Deps{
		Logger:   newTestLogger(t),
		Policies: store,
		Hydra:    unreachable,
	})

	res := f.Check(context.Background(), firewall.Call{
		SessionID: "s-down", Tool: "read_file",
		Args: map[string]any{"path": "main.go"}, ToolCost: 0.001, Budget: 2,
	})
	if res.Decision != firewall.Allow {
		t.Fatalf("Expected the existing deterministic path to still ALLOW a covered call, got %s (%s)", res.Decision, res.Reason)
	}
}
