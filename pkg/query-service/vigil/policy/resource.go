package policy

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Resource categories. A tool call is tagged with every category it touches, so
// a policy can speak in terms of capabilities ("no network") rather than having
// to enumerate tool names it may not know about.
const (
	CatFilesystemRead  = "filesystem_read"
	CatFilesystemWrite = "filesystem_write"
	CatExec            = "exec"
	CatNetwork         = "network"
	CatSecret          = "secret"
	CatObservability   = "observability"
)

// AllCategories is the closed set a policy may reference.
var AllCategories = []string{
	CatFilesystemRead, CatFilesystemWrite, CatExec, CatNetwork, CatSecret, CatObservability,
}

// toolCategories is the static capability of each built-in tool. Argument
// inspection adds more (see Categories).
var toolCategories = map[string][]string{
	"read_file":               {CatFilesystemRead},
	"list_directory":          {CatFilesystemRead},
	"search_code":             {CatFilesystemRead},
	"analyze_codebase":        {CatFilesystemRead},
	"run_command":             {CatExec},
	"signoz_query_traces":     {CatObservability},
	"signoz_get_services":     {CatObservability},
	"signoz_list_alerts":      {CatObservability},
	"signoz_create_dashboard": {CatObservability},
	"vigil_list_agents":       {CatObservability},
	"vigil_agent_dna":         {CatObservability},
	"vigil_cost_status":       {CatObservability},
}

// networkVerbs mark a shell command as reaching the network. Substring matching
// on a shell string is inherently approximate — `curl` can be spelled a dozen
// ways — so this raises the tier for review rather than being trusted as a
// complete boundary.
var networkVerbs = []string{
	"curl", "wget", "nc ", "netcat", "ssh ", "scp ", "sftp",
	"git push", "git pull", "git fetch", "git clone",
	"pip install", "npm install", "npm i ", "yarn add", "go get",
	"apt ", "apt-get", "yum ", "brew install",
}

// writeVerbs mark a shell command as mutating the filesystem.
var writeVerbs = []string{
	"rm ", "rmdir", "mv ", "cp ", "touch ", "mkdir ", "truncate",
	"tee ", " > ", " >> ", "sed -i", "chmod ", "chown ",
}

// secretPatterns mark a path or command as touching credential material.
var secretPatterns = []string{
	".env", "id_rsa", "id_ed25519", ".ssh/", ".aws/", ".gnupg/",
	"credentials", "secrets", ".netrc", ".npmrc", ".pypirc",
	".pem", ".key", ".p12", ".keystore", "kubeconfig",
}

// Categories returns every resource category a tool call touches.
//
// Argument inspection matters: run_command is not uniformly dangerous, and
// read_file is not uniformly benign. `read_file(".env")` reads a secret;
// `run_command("go test ./...")` does not reach the network. Tagging on
// arguments is what lets one policy express "may read the repo, may not read
// credentials" without enumerating paths.
func Categories(tool string, args map[string]any) []string {
	seen := map[string]bool{}
	add := func(cats ...string) {
		for _, c := range cats {
			seen[c] = true
		}
	}

	add(toolCategories[tool]...)

	switch tool {
	case "run_command":
		cmd := strings.ToLower(argString(args, "command"))
		if containsAny(cmd, networkVerbs) {
			add(CatNetwork)
		}
		if containsAny(cmd, writeVerbs) {
			add(CatFilesystemWrite)
		}
		if containsAny(cmd, secretPatterns) {
			add(CatSecret)
		}
	case "read_file", "list_directory":
		if isSecretPath(argString(args, "path")) {
			add(CatSecret)
		}
	case "search_code":
		// A search whose pattern hunts for credential material is a secret
		// read even though no single file is named.
		if containsAny(strings.ToLower(argString(args, "pattern")), []string{"api_key", "apikey", "secret", "password", "token", "private key"}) {
			add(CatSecret)
		}
	}

	// Preserve AllCategories order so output is stable and comparable.
	out := make([]string, 0, len(seen))
	for _, c := range AllCategories {
		if seen[c] {
			out = append(out, c)
		}
	}
	return out
}

// isSecretPath reports whether a path looks like credential material.
func isSecretPath(path string) bool {
	if path == "" {
		return false
	}
	lowered := strings.ToLower(path)
	if containsAny(lowered, secretPatterns) {
		return true
	}
	// Catch ".env.production" and friends, which the plain ".env" substring
	// already covers, plus bare extension matches like "server.key".
	ext := strings.ToLower(filepath.Ext(lowered))
	switch ext {
	case ".pem", ".key", ".p12", ".pfx", ".keystore":
		return true
	}
	return false
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	switch v := args[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}
