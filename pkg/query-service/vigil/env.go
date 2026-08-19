package vigil

import "os"

// Env reads a Vigil configuration value, preferring the canonical VIGIL_<name>
// variable and falling back to the legacy ARGUS_<name> spelling.
//
// The fallback exists because deployments provisioned before the ARGUS → Vigil
// rename still set ARGUS_* (render.yaml ships ARGUS_PUBLIC_BASE and
// ARGUS_DASHBOARD_BASE). It is deprecated: set VIGIL_* on new deployments.
//
// name is the suffix without a prefix, e.g. Env("PUBLIC_BASE").
func Env(name string) string {
	if v := os.Getenv("VIGIL_" + name); v != "" {
		return v
	}
	return os.Getenv("ARGUS_" + name)
}

// EnvOr is Env with a default for when neither spelling is set.
func EnvOr(name, fallback string) string {
	if v := Env(name); v != "" {
		return v
	}
	return fallback
}
