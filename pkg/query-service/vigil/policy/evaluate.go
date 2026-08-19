package policy

import "fmt"

// Outcome is a policy's answer for one tool call.
type Outcome string

const (
	// Allow: the call is within the declared intent.
	Allow Outcome = "ALLOW"
	// Block: the call contradicts the declared intent. Final — no later stage
	// of the pipeline may lift it.
	Block Outcome = "BLOCK"
	// Uncertain: the policy does not cover this call. Escalate for judgement.
	Uncertain Outcome = "UNCERTAIN"
)

// Verdict is an outcome plus the human-readable reason shown on the dashboard.
type Verdict struct {
	Outcome    Outcome  `json:"outcome"`
	Reason     string   `json:"reason"`
	Categories []string `json:"categories"`
}

// Evaluate judges one tool call against the policy.
//
// Deny wins, and denial is final. The ordering below is the policy's whole
// security semantics, so it is written as a flat sequence rather than being
// distributed across helpers: an explicit deny beats an allowlist, a capability
// ban beats a tool permit, and only an *uncovered* call becomes Uncertain.
func (p *Policy) Evaluate(tool string, args map[string]any) Verdict {
	cats := Categories(tool, args)

	// 1. Explicit tool denial.
	if contains(p.DeniedTools, tool) {
		return Verdict{Block, fmt.Sprintf("%s is denied by declared intent", tool), cats}
	}

	// 2. Explicit capability denial.
	for _, c := range cats {
		if contains(p.DeniedResources, c) {
			return Verdict{Block, fmt.Sprintf("%s access violates declared intent", humanCategory(c)), cats}
		}
	}

	// 3. Capability flags. These are separate from DeniedResources because
	//    "no network" is the common case and should not require the author to
	//    know the category vocabulary.
	if !p.NetworkAccess && contains(cats, CatNetwork) {
		return Verdict{Block, "network access violates declared intent", cats}
	}
	if !p.SecretAccess && contains(cats, CatSecret) {
		return Verdict{Block, "access to credentials violates declared intent", cats}
	}

	// 4. Allowlist miss. Not a denial — the intent simply did not anticipate
	//    this call. Under a low risk tolerance that resolves to no; otherwise
	//    it is exactly the case worth escalating for judgement.
	if len(p.AllowedTools) > 0 && !contains(p.AllowedTools, tool) {
		if p.RiskTolerance == RiskLow {
			return Verdict{Block, fmt.Sprintf("%s is not permitted by declared intent", tool), cats}
		}
		return Verdict{Uncertain, fmt.Sprintf("%s is not covered by declared intent", tool), cats}
	}

	// 5. Same treatment for a resource allowlist, when one was declared.
	if len(p.AllowedResources) > 0 {
		for _, c := range cats {
			if !contains(p.AllowedResources, c) {
				if p.RiskTolerance == RiskLow {
					return Verdict{Block, fmt.Sprintf("%s is not permitted by declared intent", humanCategory(c)), cats}
				}
				return Verdict{Uncertain, fmt.Sprintf("%s is not covered by declared intent", humanCategory(c)), cats}
			}
		}
	}

	return Verdict{Allow, fmt.Sprintf("%s is permitted by declared intent", tool), cats}
}

// humanCategory renders a category for an explanation string.
func humanCategory(c string) string {
	switch c {
	case CatFilesystemRead:
		return "file read"
	case CatFilesystemWrite:
		return "file write"
	case CatExec:
		return "shell execution"
	case CatNetwork:
		return "network"
	case CatSecret:
		return "credential"
	case CatObservability:
		return "observability"
	default:
		return c
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
