package policy_test

import (
	"strings"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/policy"
)

const validGenerated = `{
  "declared_intent": "Fix failing tests only",
  "allowed_tools": ["read_file", "run_command"],
  "denied_tools": [],
  "allowed_resources": [],
  "denied_resources": ["network"],
  "budget_usd": 2,
  "risk_tolerance": "MEDIUM",
  "network_access": false,
  "secret_access": false
}`

func TestParseGeneratedValid(t *testing.T) {
	p, _, err := policy.ParseGenerated([]byte(validGenerated), "sess-1")
	if err != nil {
		t.Fatalf("Expected valid model output to compile, got %v", err)
	}
	if p.SessionID != "sess-1" {
		t.Fatalf("Expected the server-assigned session id, got %q", p.SessionID)
	}
	if p.Active {
		t.Fatal("Expected a compiled draft policy to be inactive")
	}
	if p.NetworkAccess || p.SecretAccess {
		t.Fatal("Expected network and secret access to be denied")
	}
}

// TestRejectsUnknownField: a model that invents a key must be refused, not have
// the key silently dropped. A dropped "allow_all" would produce a policy that
// reads permissive to its author and is not.
func TestRejectsUnknownField(t *testing.T) {
	bad := strings.Replace(validGenerated, `"budget_usd": 2,`, `"budget_usd": 2, "allow_all": true,`, 1)
	if _, _, err := policy.ParseGenerated([]byte(bad), "s"); err == nil {
		t.Fatal("Expected an unknown field to be rejected")
	}
}

// TestCannotSetServerAssignedFields: session_id is not in the accepted schema,
// so a model attempting to set it is rejected outright.
func TestCannotSetServerAssignedFields(t *testing.T) {
	bad := strings.Replace(validGenerated, `"declared_intent"`, `"session_id": "victim", "declared_intent"`, 1)
	if _, _, err := policy.ParseGenerated([]byte(bad), "mine"); err == nil {
		t.Fatal("Expected model-set session_id to be rejected")
	}
}

func TestRejectsMalformedJSON(t *testing.T) {
	if _, _, err := policy.ParseGenerated([]byte(`{"declared_intent": `), "s"); err == nil {
		t.Fatal("Expected malformed JSON to be rejected")
	}
}

func TestRejectsInvalidEnum(t *testing.T) {
	bad := strings.Replace(validGenerated, `"MEDIUM"`, `"WHENEVER"`, 1)
	if _, _, err := policy.ParseGenerated([]byte(bad), "s"); err == nil {
		t.Fatal("Expected an invalid risk_tolerance to be rejected")
	}
}

func TestRejectsUnknownResourceCategory(t *testing.T) {
	bad := strings.Replace(validGenerated, `["network"]`, `["telepathy"]`, 1)
	if _, _, err := policy.ParseGenerated([]byte(bad), "s"); err == nil {
		t.Fatal("Expected an unknown resource category to be rejected")
	}
}

// TestOmittedBooleansDefaultRestrictive: absence must not read as permission.
func TestOmittedBooleansDefaultRestrictive(t *testing.T) {
	minimal := `{"declared_intent":"x","allowed_tools":[],"denied_tools":[],"allowed_resources":[],"denied_resources":[],"budget_usd":1,"risk_tolerance":"LOW"}`
	p, _, err := policy.ParseGenerated([]byte(minimal), "s")
	if err != nil {
		t.Fatalf("Expected minimal output to compile, got %v", err)
	}
	if p.NetworkAccess || p.SecretAccess {
		t.Fatal("Expected omitted capability flags to default to denied, not permitted")
	}
}

func TestStripsCodeFence(t *testing.T) {
	fenced := "```json\n" + validGenerated + "\n```"
	if _, _, err := policy.ParseGenerated([]byte(fenced), "s"); err != nil {
		t.Fatalf("Expected a fenced response to be accepted, got %v", err)
	}
}

func TestDangerousRuleDetection(t *testing.T) {
	permissive := `{
      "declared_intent": "do anything",
      "allowed_tools": [], "denied_tools": [],
      "allowed_resources": [], "denied_resources": [],
      "budget_usd": 0, "risk_tolerance": "HIGH",
      "network_access": true, "secret_access": true
    }`
	_, dangerous, err := policy.ParseGenerated([]byte(permissive), "s")
	if err != nil {
		t.Fatalf("Expected a permissive policy to compile with warnings, got %v", err)
	}
	if len(dangerous) < 4 {
		t.Fatalf("Expected several danger flags, got %d: %v", len(dangerous), dangerous)
	}

	joined := strings.ToLower(strings.Join(dangerous, " "))
	for _, want := range []string{"exfiltrat", "high risk tolerance", "every tool is permitted", "budget"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("Expected a warning mentioning %q, got %v", want, dangerous)
		}
	}
}

// TestDraftIsInertUntilConfirmed is the core guarantee: model output cannot
// reach enforcement without an explicit human action.
func TestDraftIsInertUntilConfirmed(t *testing.T) {
	s := policy.NewStore()
	p, dangerous, err := policy.ParseGenerated([]byte(validGenerated), "sess-9")
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	d := &policy.Draft{ID: "draft-1", SessionID: "sess-9", Policy: p, Dangerous: dangerous}
	s.PutDraft(d)

	// Storing a draft must not change what is enforced.
	if active := s.Get("sess-9"); active != nil {
		t.Fatal("A pending draft must not become the active policy")
	}

	confirmed, err := s.Confirm("draft-1")
	if err != nil {
		t.Fatalf("Confirm failed: %v", err)
	}
	if !confirmed.Active {
		t.Fatal("Expected a confirmed policy to be active")
	}
	if active := s.Get("sess-9"); active == nil || !active.Active {
		t.Fatal("Expected the session to be governed after confirmation")
	}

	// A draft is single-use.
	if _, err := s.Confirm("draft-1"); err == nil {
		t.Fatal("Expected a confirmed draft to be consumed")
	}
}

func TestDiscardDraftLeavesNothingEnforced(t *testing.T) {
	s := policy.NewStore()
	p, _, _ := policy.ParseGenerated([]byte(validGenerated), "sess-x")
	s.PutDraft(&policy.Draft{ID: "d", SessionID: "sess-x", Policy: p})

	if !s.DiscardDraft("d") {
		t.Fatal("Expected the draft to be discarded")
	}
	if s.Get("sess-x") != nil {
		t.Fatal("Expected nothing enforced after discarding a draft")
	}
	if _, err := s.Confirm("d"); err == nil {
		t.Fatal("Expected a discarded draft to be unconfirmable")
	}
}
