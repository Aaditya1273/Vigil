package mcp

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
)

// This file holds the containment applied to the filesystem and shell tools.
//
// It is a backstop, not the governance layer. Intent policy is what decides
// whether a given session may touch a given resource category at all; these
// checks are the floor that holds even when no policy has been declared, and
// they are deliberately dumb and absolute.

var errOutsideRoot = errors.New("path resolves outside the project root")

// resolveInRoot turns a caller-supplied path into an absolute one and confirms
// it stays inside projectRoot.
//
// The previous check rejected any path containing "..", which reads as
// traversal protection but is not: it let through every absolute path on the
// host, so read_file("/etc/passwd") succeeded. Resolving symlinks matters for
// the same reason — a symlink inside the root pointing out of it would
// otherwise pass a prefix test.
func (s *MCPServer) resolveInRoot(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	if s.projectRoot == "" {
		// No root configured: fall back to rejecting relative traversal only.
		// Nothing in the shipped server does this, but tests construct servers
		// without a root and should not be silently unconstrained.
		if strings.Contains(path, "..") {
			return "", errOutsideRoot
		}
		return path, nil
	}

	root, err := filepath.Abs(s.projectRoot)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	full = filepath.Clean(full)

	// Resolve symlinks when the target exists. A non-existent path is fine to
	// leave unresolved — the prefix test below still applies, and the caller
	// will get a normal "no such file" from the operation itself.
	if resolved, err := filepath.EvalSymlinks(full); err == nil {
		full = resolved
	}

	if full != root && !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", errOutsideRoot
	}
	return full, nil
}

// execEnabled reports whether the shell tool may run at all.
//
// Defaults to false. run_command executes `bash -c` on caller-supplied input,
// and the MCP endpoint is reachable without authentication in the default
// build, so an on-by-default shell is remote code execution. Operators who
// want it must say so explicitly.
func execEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(vigil.Env("ALLOW_EXEC")))
	return v == "1" || v == "true" || v == "yes"
}

// destructivePatterns are refused even when exec is enabled. This is a floor
// against catastrophic and irreversible commands, not a security boundary —
// shell quoting has more ways to spell any of these than a list can enumerate.
// Real containment is the enabled/disabled gate above plus intent policy.
var destructivePatterns = []string{
	"rm -rf /",
	"rm -fr /",
	":(){",      // fork bomb
	"mkfs",      // filesystem format
	"dd if=",    // raw device write
	"> /dev/sd", // direct disk write
	"chmod 777 /",
	"chown -R root /",
	"shutdown",
	"reboot",
	"halt ",
}

// checkCommand reports why a command must not run, or "" if it may.
func checkCommand(command string) string {
	if !execEnabled() {
		return "run_command is disabled by configuration. Set VIGIL_ALLOW_EXEC=true to enable shell execution for this deployment."
	}
	lowered := strings.ToLower(command)
	for _, pat := range destructivePatterns {
		if strings.Contains(lowered, pat) {
			return fmt.Sprintf("refused: command matches a destructive pattern (%q)", pat)
		}
	}
	return ""
}
