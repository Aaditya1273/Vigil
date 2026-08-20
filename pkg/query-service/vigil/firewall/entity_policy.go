package firewall

import (
	"context"
	"regexp"
	"strings"
)

// shareVerbRe pulls a recipient name out of a file-sharing-shaped shell
// command: "share the report with Sam", "email the export to S. Ratnaparkhi",
// "send the file to Jordan Blake". Same caveat as extractPackageName in
// hydra.go: heuristic string matching on a shell command, raises scrutiny,
// is not a parser.
var shareVerbRe = regexp.MustCompile(`(?i)\b(?:share|send|email|mail)\b.*?\b(?:with|to)\s+([A-Z][a-zA-Z.'-]*(?:\s+[A-Z][a-zA-Z.'-]*){0,2})`)

func extractShareTarget(c Call) string {
	// A dedicated share_file-shaped tool (if one exists) names the
	// recipient directly — check that first, since it needs no guessing.
	for _, key := range []string{"recipient", "share_with", "to"} {
		if v, ok := c.Args[key].(string); ok && v != "" {
			return v
		}
	}
	if c.Tool != "run_command" {
		return ""
	}
	cmd, _ := c.Args["command"].(string)
	m := shareVerbRe.FindStringSubmatch(cmd)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// EntityPolicyReport is the structured "who is this, and why were they
// denied" answer attached to an entity-policy BLOCK — the alias-merge
// explanation a reviewer actually needs: not just "blocked", but "blocked
// because this name resolves to a Customer, and here's the chain of
// aliases that got us there."
type EntityPolicyReport struct {
	Name            string   `json:"name"`
	ResolvedAliases []string `json:"resolved_aliases"`
	EntityType      string   `json:"entity_type,omitempty"`
	PolicyPaths     []string `json:"policy_paths"`
	QueryTimeMS     float64  `json:"query_time_ms"`
}

// protectedEntityTypes are checked against the extracted predicate/target
// text to decide whether a resolved entity is a protected type. "customer"
// is the one named in the task; kept as a short, explicit list rather than
// open-ended classification — a name the graph can't characterize this way
// is UNCERTAIN, not silently assumed safe.
var protectedEntityTypes = []string{"customer"}

// checkEntityPolicy asks HydraDB's enterprise graph who a referenced share
// recipient actually is — resolving through whatever SAME_AS aliases the
// resolver (scripts/ingest_enterprise.py) already extracted — and whether
// the entity type they resolve to is one policy denies sharing with.
//
// A share-shaped call with no HydraDB configured, or one the graph can't
// resolve, is NOT blocked here: this check can tighten a decision, it is
// not itself a complete access-control system, same posture as every other
// graph consult in this pipeline. It composes with, not replaces, the
// existing declared-intent policy check.
func (f *Firewall) checkEntityPolicy(ctx context.Context, c Call) (blocked bool, report *EntityPolicyReport) {
	name := extractShareTarget(c)
	if name == "" || !f.deps.Hydra.Configured() {
		return false, nil
	}

	profile, err := f.deps.Hydra.GetEntityProfile(ctx, name)
	if err != nil {
		f.deps.Logger.WarnContext(ctx, "vigil: entity profile lookup failed", "name", name, "error", err.Error())
		return false, nil
	}

	rep := &EntityPolicyReport{
		Name:            name,
		ResolvedAliases: profile.Aliases(),
		PolicyPaths:     profile.PolicyPaths,
		QueryTimeMS:     profile.QueryTimeMS,
	}

	// Candidate names this entity could appear as the subject of an
	// entity-type triplet under — the queried name plus every resolved
	// alias, so "share with Sam" still matches a triplet whose subject is
	// the canonical "Soham Ratnaparkhi".
	candidates := []string{strings.ToLower(name)}
	for _, a := range rep.ResolvedAliases {
		candidates = append(candidates, strings.ToLower(a))
	}

	// Real bug found by querying the live graph with two different real
	// people: entity-type detection used to match "customer" anywhere in
	// the retrieved policy text, but that text is dominated by the policy
	// documents themselves ("policy no-pii-exfil applies to entity type
	// Customer") which mention "customer" regardless of who was asked
	// about — so *every* name got flagged as a Customer. An entity-type
	// triplet (e.g. "jordan blake --[customer of]--> northwind signal")
	// must actually name this entity as its subject before it counts.
	// The deny signal stays a general keyword scan: it describes what the
	// policy for a type does, not who the type applies to.
	deny := false
	for _, p := range profile.PolicyPaths {
		lower := strings.ToLower(p)
		// "denies" (present tense) is HydraDB's real extracted predicate
		// for a denial and is not a substring of "denied" — same bug just
		// found and fixed in hydraIntentCheck (hydra.go), caught here by
		// the live test against the real graph, not the stub.
		if strings.Contains(lower, "deny") || strings.Contains(lower, "denies") || strings.Contains(lower, "denied") || strings.Contains(lower, "forbid") {
			deny = true
		}
		srcEnd := strings.Index(p, " --[")
		if srcEnd <= 0 {
			continue
		}
		subject := strings.ToLower(p[:srcEnd])
		named := false
		for _, cand := range candidates {
			if strings.Contains(subject, cand) || strings.Contains(cand, subject) {
				named = true
				break
			}
		}
		if !named {
			continue
		}
		for _, t := range protectedEntityTypes {
			if strings.Contains(lower, t) {
				rep.EntityType = t
			}
		}
	}

	if rep.EntityType != "" && deny {
		return true, rep
	}
	return false, rep
}
