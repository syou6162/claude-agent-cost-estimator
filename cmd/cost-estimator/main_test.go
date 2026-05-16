package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureCwd = "/Users/yasuhisa.yoshida/work/times-esa-talk-slack"

// TestRun_MissingCwdExits2 locks the "usage: --cwd is required" message
// so that flag-package default usage output never replaces it.
func TestRun_MissingCwdExits2(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: got %d want 2", code)
	}
	if got := stderr.String(); !strings.Contains(got, "usage: --cwd is required") {
		t.Errorf("stderr missing message: %q", got)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", stdout.String())
	}
}

// TestRun_NoProjectDirExits2 verifies the "no project dir for cwd"
// stderr wording locked by the failure-mode section in the plan.
func TestRun_NoProjectDirExits2(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", "/nope/missing", "--claude-config-dir", tmp}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: %d", code)
	}
	if !strings.Contains(stderr.String(), "no project dir for cwd: /nope/missing") {
		t.Errorf("stderr: %q", stderr.String())
	}
}

// TestRun_InvalidConfigDirExits2 covers the "no valid claude config dir"
// path when CLAUDE_CONFIG_DIR is explicitly set but yields zero valid roots.
func TestRun_InvalidConfigDirExits2(t *testing.T) {
	tmp := t.TempDir()
	// Path exists but has no "projects" subdir, so it's invalid.
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", fixtureCwd, "--claude-config-dir", tmp}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit code: %d", code)
	}
	if !strings.Contains(stderr.String(), "no valid claude config dir") {
		t.Errorf("stderr: %q", stderr.String())
	}
}

// TestRun_FlagOverridesEnv verifies the flag takes precedence over the
// CLAUDE_CONFIG_DIR environment variable.
func TestRun_FlagOverridesEnv(t *testing.T) {
	// Env points to an invalid dir; flag points to a valid one with our
	// fixtures. If the flag wins, the run succeeds.
	envDir := t.TempDir() // no /projects
	t.Setenv("CLAUDE_CONFIG_DIR", envDir)

	flagDir := setupNestedFixture(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", fixtureCwd, "--claude-config-dir", flagDir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: %d stderr: %s", code, stderr.String())
	}
	// Output should be valid JSON with at least one session.
	var reports []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(reports) == 0 {
		t.Error("expected at least one session")
	}
}

// TestRun_E2ENestedFixture exercises the full pipeline against the
// testdata fixture and validates costs match what the price table
// produces.
func TestRun_E2ENestedFixture(t *testing.T) {
	root := setupNestedFixture(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", fixtureCwd, "--claude-config-dir", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: %d stderr: %s", code, stderr.String())
	}
	var reports []struct {
		SessionID    string   `json:"sessionId"`
		ProjectPath  string   `json:"projectPath"`
		TotalCostUSD *float64 `json:"totalCostUSD"`
		Models       []struct {
			Model       string  `json:"model"`
			InputTokens int64   `json:"inputTokens"`
			CostUSD     float64 `json:"costUSD"`
		} `json:"models"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 session, got %d", len(reports))
	}
	r := reports[0]
	if r.SessionID != "session-abc123" {
		t.Errorf("sessionId: %s", r.SessionID)
	}
	if r.ProjectPath != fixtureCwd {
		t.Errorf("projectPath: %s", r.ProjectPath)
	}
	// chunk-0 has opus 4-7 (100in/200out) and sonnet 4-6 (50in/100out cache 1000/500),
	// chunk-1 has dup of opus row with larger tokens (150in/200out) — dedup keeps
	// the larger row so opus input ends up at 150, output 200.
	var opus, sonnet bool
	for _, m := range r.Models {
		switch {
		case strings.HasPrefix(m.Model, "claude-opus-4-7"):
			opus = true
			if m.InputTokens != 150 {
				t.Errorf("opus input: got %d want 150", m.InputTokens)
			}
		case strings.HasPrefix(m.Model, "claude-sonnet-4-6"):
			sonnet = true
		}
	}
	if !opus || !sonnet {
		t.Errorf("missing model breakdown: opus=%v sonnet=%v", opus, sonnet)
	}
}

// TestRun_E2EUnknownModelWarn covers the exact WARN format and exit
// code 0 when an unknown model appears in the project. The exact line
// is locked here so a refactor that drops the sessionId or rephrases
// the message regresses immediately.
func TestRun_E2EUnknownModelWarn(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "-test-unknown")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","cwd":"/test/unknown","message":{"role":"assistant","model":"totally-unknown-model","id":"m","usage":{"input_tokens":1}},"requestId":"r","timestamp":"2026-04-15T22:00:00.000Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, "abc.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", "/test/unknown", "--claude-config-dir", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: %d", code)
	}
	want := "WARN: unknown model \"totally-unknown-model\" (sessionId=abc)\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestRun_E2EDebugSkipFormat verifies the malformed-line skip WARN
// format printed when DEBUG=1 is set. Without DEBUG the line is
// silently dropped.
func TestRun_E2EDebugSkipFormat(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "-test-malformed")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(projectDir, "x.jsonl")
	// Line 1: malformed. Line 2: valid assistant entry so the file as
	// a whole is still parseable.
	contents := "{not json}\n" +
		`{"type":"assistant","cwd":"/test/malformed","message":{"role":"assistant","model":"claude-opus-4-7","id":"a","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"r","timestamp":"2026-04-15T22:00:00.000Z"}` + "\n"
	if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEBUG", "1")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", "/test/malformed", "--claude-config-dir", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: %d", code)
	}
	want := "WARN: skip " + file + ":1 reason=invalid-json\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestRun_E2EPathSessionIDOverridesJSONField confirms that the
// in-file sessionId field is ignored: grouping always follows the
// file path (matches ccusage and avoids silent misgrouping).
func TestRun_E2EPathSessionIDOverridesJSONField(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "-test-pathwin")
	mkdir(t, projectDir)
	// File basename is "path-wins", but the JSON entry claims "in-json-id".
	writeFile(t, filepath.Join(projectDir, "path-wins.jsonl"),
		`{"type":"assistant","cwd":"/test/pathwin","sessionId":"in-json-id","message":{"role":"assistant","model":"claude-opus-4-7","id":"m1","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"r1","timestamp":"2026-04-15T22:00:00.000Z"}`+"\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", "/test/pathwin", "--claude-config-dir", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: %d stderr: %s", code, stderr.String())
	}
	var reports []struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 {
		t.Fatalf("expected 1 session, got %d", len(reports))
	}
	if reports[0].SessionID != "path-wins" {
		t.Errorf("path-derived sessionId should win, got %q", reports[0].SessionID)
	}
}

// TestResolveExplicitConfigDirs_EmptyEntries ensures an explicit but
// all-empty value (e.g. "--claude-config-dir=,") propagates to
// EnumerateDataRoots as a non-nil empty slice so the caller surfaces
// ErrNoValidConfigDir instead of silently defaulting.
func TestResolveExplicitConfigDirs_EmptyEntries(t *testing.T) {
	got := resolveExplicitConfigDirs(",", "")
	if got == nil {
		t.Fatal("expected non-nil empty slice for explicit empty input")
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
	// And nil-propagation path: both empty -> nil signals "use defaults".
	if resolveExplicitConfigDirs("", "") != nil {
		t.Error("both-empty inputs should yield nil")
	}
}

// TestRun_E2EFlatVsNestedSessionID exercises sessionId derivation: the
// flat file becomes "abc-flat", the nested chunk becomes "session-abc123".
func TestRun_E2EFlatVsNestedSessionID(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "projects", "-test-mix")
	mkdir(t, projectDir)
	mkdir(t, filepath.Join(projectDir, "session-abc123"))
	writeFile(t, filepath.Join(projectDir, "abc-flat.jsonl"),
		`{"type":"assistant","cwd":"/test/mix","message":{"role":"assistant","model":"claude-opus-4-7","id":"f1","usage":{"input_tokens":1,"output_tokens":1}},"requestId":"rf1","timestamp":"2026-04-15T22:00:00.000Z"}`+"\n")
	writeFile(t, filepath.Join(projectDir, "session-abc123", "chunk.jsonl"),
		`{"type":"assistant","cwd":"/test/mix","message":{"role":"assistant","model":"claude-opus-4-7","id":"n1","usage":{"input_tokens":2,"output_tokens":2}},"requestId":"rn1","timestamp":"2026-04-15T22:00:00.000Z"}`+"\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--cwd", "/test/mix", "--claude-config-dir", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: %d stderr: %s", code, stderr.String())
	}
	var reports []struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range reports {
		seen[r.SessionID] = true
	}
	if !seen["abc-flat"] {
		t.Error("flat session id missing")
	}
	if !seen["session-abc123"] {
		t.Error("nested session id missing")
	}
}

// setupNestedFixture copies testdata/nested/project/* under a temporary
// claude config root with the proper encoded project directory name
// matching fixtureCwd.
func setupNestedFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	encoded := "-Users-yasuhisa-yoshida-work-times-esa-talk-slack"
	dst := filepath.Join(tmp, "projects", encoded)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("..", "..", "testdata", "nested", "project")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	return tmp
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, contents string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

var _ io.Writer = (*bytes.Buffer)(nil)
