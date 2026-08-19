package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SigNoz/signoz/pkg/query-service/vigil/mcp"
)

// resultText pulls the text out of a tools/call response.
func resultText(t *testing.T, resp *mcp.Response) string {
	t.Helper()
	if resp == nil {
		t.Fatal("Expected a response, got nil")
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	var r struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

func TestRunCommandDisabledByDefault(t *testing.T) {
	t.Setenv("VIGIL_ALLOW_EXEC", "")
	t.Setenv("ARGUS_ALLOW_EXEC", "")
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("run_command", map[string]any{"command": "echo hello"}), "x"))

	if !strings.Contains(got, "disabled by configuration") {
		t.Fatalf("Expected run_command to be refused by default, got %q", got)
	}
}

func TestRunCommandRefusesDestructivePattern(t *testing.T) {
	t.Setenv("VIGIL_ALLOW_EXEC", "true")
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("run_command", map[string]any{"command": "sudo rm -rf / --no-preserve-root"}), "x"))

	if !strings.Contains(got, "destructive pattern") {
		t.Fatalf("Expected a destructive command to be refused even with exec enabled, got %q", got)
	}
}

func TestRunCommandRunsWhenEnabled(t *testing.T) {
	t.Setenv("VIGIL_ALLOW_EXEC", "true")
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())

	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("run_command", map[string]any{"command": "echo vigil-ok"}), "x"))

	if !strings.Contains(got, "vigil-ok") {
		t.Fatalf("Expected the command to run when explicitly enabled, got %q", got)
	}
}

// TestReadFileRejectsAbsoluteEscape is the important one: the old check only
// rejected paths containing "..", so any absolute path on the host was readable.
func TestReadFileRejectsAbsoluteEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("classified"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	s := mcp.NewMCPServer(newTestLogger(t), root)
	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("read_file", map[string]any{"path": outside}), "x"))

	if strings.Contains(got, "classified") {
		t.Fatalf("read_file escaped the project root and returned file contents: %q", got)
	}
	if !strings.Contains(got, "outside the project root") {
		t.Fatalf("Expected an out-of-root rejection, got %q", got)
	}
}

func TestReadFileRejectsRelativeTraversal(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())
	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("read_file", map[string]any{"path": "../../../etc/passwd"}), "x"))

	if !strings.Contains(got, "outside the project root") {
		t.Fatalf("Expected traversal to be rejected, got %q", got)
	}
}

func TestReadFileAllowsInsideRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	s := mcp.NewMCPServer(newTestLogger(t), root)
	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("read_file", map[string]any{"path": "ok.txt"}), "x"))

	if !strings.Contains(got, "inside") {
		t.Fatalf("Expected to read a file inside the root, got %q", got)
	}
}

func TestListDirectoryRejectsEscape(t *testing.T) {
	s := mcp.NewMCPServer(newTestLogger(t), t.TempDir())
	got := resultText(t, s.HandleRequest(context.Background(),
		toolCall("list_directory", map[string]any{"path": "/etc"}), "x"))

	if !strings.Contains(got, "outside the project root") {
		t.Fatalf("Expected list_directory to reject an out-of-root path, got %q", got)
	}
}
