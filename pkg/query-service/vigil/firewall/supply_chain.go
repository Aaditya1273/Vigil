package firewall

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/hydra"
)

// CompromisedList is an incident-response denylist: package names (or
// name@version pairs) an operator has confirmed compromised, checked before
// the graph-based typosquat heuristic runs. This is what a real response to
// something like the TanStack worm looks like operationally — security
// publishes "these N packages, these M versions are bad" within minutes of
// discovery, and every install of any of them needs to stop immediately,
// not wait for a graph query to notice something's wrong.
//
// A concurrency-safe set rather than a static list because an operator
// updating it during an active incident must not race the firewall reading
// it mid-call.
type CompromisedList struct {
	mu    sync.RWMutex
	exact map[string]bool // "name@version"
	names map[string]bool // "name" (any version blocked)
}

// NewCompromisedList parses VIGIL_COMPROMISED_PACKAGES: a comma-separated
// list of "name" or "name@version" entries. Empty/unset is a normal,
// supported state — no active incident, nothing on the list.
func NewCompromisedList() *CompromisedList {
	l := &CompromisedList{exact: map[string]bool{}, names: map[string]bool{}}
	l.LoadEnv()
	return l
}

// LoadEnv re-reads VIGIL_COMPROMISED_PACKAGES. Exported so an operator tool
// (or a future admin endpoint) can push an update into a running process
// without a restart — the whole point of an incident-response list is that
// it changes faster than a deploy cycle.
func (l *CompromisedList) LoadEnv() {
	raw := os.Getenv("VIGIL_COMPROMISED_PACKAGES")
	exact := map[string]bool{}
	names := map[string]bool{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "@") && !strings.HasPrefix(entry, "@") {
			exact[strings.ToLower(entry)] = true
		} else {
			names[strings.ToLower(strings.TrimPrefix(entry, "@"))] = true
		}
	}
	l.mu.Lock()
	l.exact, l.names = exact, names
	l.mu.Unlock()
}

// Check reports whether pkg (optionally @version) is on the list.
func (l *CompromisedList) Check(pkg, version string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	pkg = strings.ToLower(pkg)
	if version != "" && l.exact[pkg+"@"+strings.ToLower(version)] {
		return true
	}
	return l.names[pkg]
}

// Empty reports whether any packages are currently listed — used to skip
// the check entirely rather than pay a map lookup on every call for the
// overwhelmingly common case of no active incident.
func (l *CompromisedList) Empty() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.exact) == 0 && len(l.names) == 0
}

// SupplyChainReport is the structured blast-radius answer attached to a
// supply-chain BLOCK — real names extracted from the real graph paths
// GetFullBlastRadius returned, not a template. Every field is what it
// literally says: ExposedServices is genuinely empty if the graph found no
// dependents, not a placeholder value.
type SupplyChainReport struct {
	ExposedServices   []string `json:"exposed_services"`
	MaintainerShared  []string `json:"maintainer_shared"`
	Typosquats        []string `json:"typosquats"`
	BlastRadiusTimeMS float64  `json:"blast_radius_time_ms"`
}

func toReport(br hydra.BlastRadius) SupplyChainReport {
	return SupplyChainReport{
		ExposedServices:   br.ExposedServices(),
		MaintainerShared:  br.SharedMaintainers(),
		Typosquats:        br.Typosquats(),
		BlastRadiusTimeMS: br.QueryTimeMS,
	}
}

// checkSupplyChain runs before the general blast-radius typosquat check
// (hydraBlastRadius): a package already on the compromised list is blocked
// unconditionally — no graph query needed to know the answer — but the full
// blast-radius report is still fetched and attached, because "blocked" isn't
// useful to an incident responder without "and here's what was exposed".
func (f *Firewall) checkSupplyChain(ctx context.Context, c Call) (blocked bool, report *SupplyChainReport, pkg string) {
	pkg = blastRadiusTarget(c)
	if pkg == "" || f.deps.Compromised == nil || f.deps.Compromised.Empty() {
		return false, nil, pkg
	}
	name, version := splitPkgVersion(pkg)
	if !f.deps.Compromised.Check(name, version) {
		return false, nil, pkg
	}
	if !f.deps.Hydra.Configured() {
		return true, nil, pkg
	}
	br, err := f.deps.Hydra.GetFullBlastRadius(ctx, name, "the active incident window")
	if err != nil {
		f.deps.Logger.WarnContext(ctx, "vigil: blast radius lookup failed for a compromised package", "package", pkg, "error", err.Error())
		return true, nil, pkg
	}
	rep := toReport(br)
	return true, &rep, pkg
}

// splitPkgVersion splits "name@version" (scoped-safe: a leading @scope/name
// is not itself a version separator).
func splitPkgVersion(pkg string) (name, version string) {
	body := pkg
	prefix := ""
	if strings.HasPrefix(pkg, "@") {
		if i := strings.Index(pkg, "/"); i > 0 {
			prefix, body = pkg[:i+1], pkg[i+1:]
		}
	}
	if i := strings.LastIndex(body, "@"); i > 0 {
		return prefix + body[:i], body[i+1:]
	}
	return pkg, ""
}
