package appserver

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
)

// The control endpoints — kill, pause, resume, block a session, raise a budget —
// were reachable by anyone who could reach the host, behind a permissive CORS
// policy. A firewall whose kill switch is itself unauthenticated is not a
// firewall.
//
// The guard is a shared secret rather than full identity because Vigil has no
// user model: there is nobody to authenticate *as*. A bearer secret is the
// honest primitive for "the operator, not the internet", and it composes with
// whatever real auth sits in front in a production deployment.
//
// Absent VIGIL_CONTROL_TOKEN the guard is open and says so loudly at startup.
// That keeps `make demo` and a local dashboard working with zero setup, which
// is the difference between a security control people adopt and one they
// disable. Deployments that bind anywhere but loopback should set it.

// controlToken returns the configured operator secret, or "" when unset.
func controlToken() string { return vigil.Env("CONTROL_TOKEN") }

// ControlAuthEnabled reports whether the control endpoints are protected.
func ControlAuthEnabled() bool { return controlToken() != "" }

// requireControlAuth wraps a mutating control handler.
func requireControlAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Preflight carries no credentials by design.
		if r.Method == http.MethodOptions {
			next(w, r)
			return
		}

		want := controlToken()
		if want == "" {
			next(w, r)
			return
		}

		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" {
			got = r.Header.Get("X-Vigil-Control-Token")
		}

		// Constant-time compare: a byte-wise early return leaks the secret one
		// character at a time to anyone willing to measure.
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", `Bearer realm="vigil-control"`)
			w.WriteHeader(http.StatusUnauthorized)
			// Deliberately terse: no hint about length, prefix, or whether a
			// token was supplied at all.
			json.NewEncoder(w).Encode(map[string]string{
				"error": "control endpoints require a valid operator token",
			})
			return
		}
		next(w, r)
	}
}
