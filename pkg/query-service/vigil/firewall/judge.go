package firewall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// judgment is a model's security assessment of one tool call.
//
// Pointer fields for the required scalars so a missing key is distinguishable
// from a zero value: an omitted risk_score must not read as 0 (safe), and an
// omitted intent_violation must not read as false.
type judgment struct {
	RiskScore       *int     `json:"risk_score"`
	Severity        string   `json:"severity"`
	Decision        string   `json:"decision"`
	Reasons         []string `json:"reasons"`
	IntentViolation *bool    `json:"intent_violation"`
	Confidence      *float64 `json:"confidence"`
}

var (
	validSeverity = map[string]bool{"LOW": true, "MEDIUM": true, "HIGH": true, "CRITICAL": true}
	validDecision = map[string]bool{"ALLOW": true, "PAUSE": true, "BLOCK": true, "FALLBACK": true}
)

const judgeSystemPrompt = `You are a security reviewer for an AI agent's tool call.

Return ONLY a JSON object with exactly these keys:
{
  "risk_score": integer 0-100,
  "severity": "LOW" | "MEDIUM" | "HIGH" | "CRITICAL",
  "decision": "ALLOW" | "PAUSE" | "BLOCK" | "FALLBACK",
  "reasons": [string],
  "intent_violation": boolean,
  "confidence": number 0-1
}

Judge whether the call is consistent with the agent's declared intent and
whether it presents a security or cost risk. Every key is required. Emit no
prose, no markdown, and no code fences.`

// parseJudgment validates raw model output.
//
// Pure and byte-taking so every rejection path is unit-testable without a
// credential. Nothing here is lenient except the code-fence strip: a model that
// returns an out-of-range score or an unknown enum is treated as broken, and
// the caller falls back to deterministic rules.
func parseJudgment(raw []byte) (judgment, error) {
	var j judgment

	dec := json.NewDecoder(bytes.NewReader(stripFence(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return j, fmt.Errorf("unparseable judgment: %w", err)
	}

	// Presence. A missing decision is not an implicit allow.
	if j.RiskScore == nil {
		return j, errors.New("missing risk_score")
	}
	if j.IntentViolation == nil {
		return j, errors.New("missing intent_violation")
	}
	if j.Confidence == nil {
		return j, errors.New("missing confidence")
	}
	if j.Decision == "" {
		return j, errors.New("missing decision")
	}
	if j.Severity == "" {
		return j, errors.New("missing severity")
	}

	// Enum membership.
	j.Severity = strings.ToUpper(strings.TrimSpace(j.Severity))
	j.Decision = strings.ToUpper(strings.TrimSpace(j.Decision))
	if !validSeverity[j.Severity] {
		return j, fmt.Errorf("invalid severity %q", j.Severity)
	}
	if !validDecision[j.Decision] {
		return j, fmt.Errorf("invalid decision %q", j.Decision)
	}

	// Range. An out-of-range score means the model is not answering the
	// question that was asked, so nothing it returned is trustworthy.
	if *j.RiskScore < 0 || *j.RiskScore > 100 {
		return j, fmt.Errorf("risk_score %d out of range 0-100", *j.RiskScore)
	}
	if *j.Confidence < 0 || *j.Confidence > 1 {
		return j, fmt.Errorf("confidence %v out of range 0-1", *j.Confidence)
	}

	return j, nil
}

// stripFence removes a surrounding markdown code fence.
func stripFence(raw []byte) []byte {
	s := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(s, "```") {
		return []byte(s)
	}
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return []byte(strings.TrimSpace(s))
}

// score returns the validated risk score.
func (j judgment) score() int {
	if j.RiskScore == nil {
		return 0
	}
	return *j.RiskScore
}

func (j judgment) reason() string {
	if len(j.Reasons) == 0 {
		return "model returned no reason"
	}
	return strings.Join(j.Reasons, "; ")
}
