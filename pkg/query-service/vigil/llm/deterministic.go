package llm

import (
	"context"
	"os"
)

// osGetenv is a thin indirection so featherless.go can read a bare
// FEATHERLESS_* variable without importing os for one call.
func osGetenv(k string) string { return os.Getenv(k) }

// DeterministicProvider stands in when no inference credentials are configured.
//
// It returns ErrNoModel rather than a synthesized verdict. That is the whole
// point: every caller already has a fail-closed deterministic path for
// timeouts and malformed responses, so "no credential configured" travels the
// exact same code as "the provider broke". A stub that invented a risk score
// would make an unconfigured deployment look like a working one, and would put
// a fabricated number into the audit chain.
type DeterministicProvider struct{}

func (DeterministicProvider) Name() string { return "deterministic" }

func (DeterministicProvider) Configured(Role) bool { return false }

func (DeterministicProvider) Complete(context.Context, Request) (*Response, error) {
	return nil, ErrNoModel
}
