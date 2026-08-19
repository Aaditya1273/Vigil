package firewall

import (
	"strings"
	"testing"
)

const validJudgment = `{
  "risk_score": 82,
  "severity": "HIGH",
  "decision": "BLOCK",
  "reasons": ["network egress contradicts declared intent"],
  "intent_violation": true,
  "confidence": 0.9
}`

func TestParseValidJudgment(t *testing.T) {
	j, err := parseJudgment([]byte(validJudgment))
	if err != nil {
		t.Fatalf("Expected valid output to parse, got %v", err)
	}
	if j.score() != 82 || j.Severity != "HIGH" || j.Decision != "BLOCK" {
		t.Fatalf("Parsed values wrong: %+v", j)
	}
	if !strings.Contains(j.reason(), "network egress") {
		t.Fatalf("Expected the reason to be carried through, got %q", j.reason())
	}
}

func TestParseMalformedJSON(t *testing.T) {
	if _, err := parseJudgment([]byte(`{"risk_score": `)); err == nil {
		t.Fatal("Expected malformed JSON to be rejected")
	}
}

func TestParseEmptyResponse(t *testing.T) {
	if _, err := parseJudgment([]byte("")); err == nil {
		t.Fatal("Expected an empty response to be rejected")
	}
}

// TestParseMissingFieldsRejected: absence must never be read as a safe value.
// A missing decision is not an implicit allow; a missing risk_score is not 0.
func TestParseMissingFieldsRejected(t *testing.T) {
	cases := map[string]string{
		"risk_score":       `{"severity":"LOW","decision":"ALLOW","reasons":[],"intent_violation":false,"confidence":0.5}`,
		"decision":         `{"risk_score":1,"severity":"LOW","reasons":[],"intent_violation":false,"confidence":0.5}`,
		"severity":         `{"risk_score":1,"decision":"ALLOW","reasons":[],"intent_violation":false,"confidence":0.5}`,
		"intent_violation": `{"risk_score":1,"severity":"LOW","decision":"ALLOW","reasons":[],"confidence":0.5}`,
		"confidence":       `{"risk_score":1,"severity":"LOW","decision":"ALLOW","reasons":[],"intent_violation":false}`,
	}
	for field, body := range cases {
		if _, err := parseJudgment([]byte(body)); err == nil {
			t.Fatalf("Expected a missing %s to be rejected", field)
		}
	}
}

func TestParseInvalidEnums(t *testing.T) {
	bad := strings.Replace(validJudgment, `"HIGH"`, `"SPICY"`, 1)
	if _, err := parseJudgment([]byte(bad)); err == nil {
		t.Fatal("Expected an invalid severity to be rejected")
	}
	bad = strings.Replace(validJudgment, `"BLOCK"`, `"MAYBE"`, 1)
	if _, err := parseJudgment([]byte(bad)); err == nil {
		t.Fatal("Expected an invalid decision to be rejected")
	}
}

// TestParseOutOfRangeRejected: a score outside 0-100 means the model is not
// answering the question asked, so nothing it returned is trustworthy.
func TestParseOutOfRangeRejected(t *testing.T) {
	for _, bad := range []string{
		strings.Replace(validJudgment, "82", "140", 1),
		strings.Replace(validJudgment, "82", "-3", 1),
		strings.Replace(validJudgment, "0.9", "1.5", 1),
		strings.Replace(validJudgment, "0.9", "-0.2", 1),
	} {
		if _, err := parseJudgment([]byte(bad)); err == nil {
			t.Fatalf("Expected an out-of-range value to be rejected: %s", bad)
		}
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	bad := strings.Replace(validJudgment, `"confidence": 0.9`, `"confidence": 0.9, "override": true`, 1)
	if _, err := parseJudgment([]byte(bad)); err == nil {
		t.Fatal("Expected an unknown field to be rejected")
	}
}

// TestParseStripsCodeFence: models emit fences habitually, and failing closed
// over a formatting tic when the content is still strictly validated is a bad
// trade.
func TestParseStripsCodeFence(t *testing.T) {
	if _, err := parseJudgment([]byte("```json\n" + validJudgment + "\n```")); err != nil {
		t.Fatalf("Expected a fenced response to parse, got %v", err)
	}
	if _, err := parseJudgment([]byte("```\n" + validJudgment + "\n```")); err != nil {
		t.Fatalf("Expected an unlabeled fence to parse, got %v", err)
	}
}

func TestParseAcceptsBoundaryValues(t *testing.T) {
	for _, body := range []string{
		`{"risk_score":0,"severity":"LOW","decision":"ALLOW","reasons":[],"intent_violation":false,"confidence":0}`,
		`{"risk_score":100,"severity":"CRITICAL","decision":"BLOCK","reasons":[],"intent_violation":true,"confidence":1}`,
	} {
		if _, err := parseJudgment([]byte(body)); err != nil {
			t.Fatalf("Expected boundary values to be accepted, got %v", err)
		}
	}
}
