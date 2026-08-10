// Package test exercises the `topic` CLI as a black box.
//
// Testing the CLI contract rather than the implementation is deliberate: these tests
// drive whatever `bin/topic` happens to be — bash today, Go as commands are migrated —
// so they double as the specification and the acceptance criteria for that migration.
//
// Two tiers:
//
//	hermetic — a sandboxed registry and config dir, no session ever launched. Fast,
//	           safe, and where most behaviour lives. Runs always.
//	live     — really launches Claude Code, because the initialisation behaviour
//	           (trust prompts, first-run setup, Remote Control bridges) is exactly
//	           what has bitten us and cannot be faked convincingly. Needs a real
//	           logged-in profile: credentials are per-profile, so a throwaway config
//	           dir is NOT logged in. Set TOPIC_TEST_PROFILE, or they skip.
package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// topicBin is the script under test, resolved relative to this file so `go test`
// works from any directory.
func topicBin(t *testing.T) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}
	bin := filepath.Join(filepath.Dir(filepath.Dir(self)), "bin", "topic")
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("topic binary not found at %s: %v", bin, err)
	}
	return bin
}

// env is one sandboxed world for a test: its own registry and config dir, so nothing
// touches the developer's real topics.
type env struct {
	t          *testing.T
	configDir  string // CLAUDE_CONFIG_DIR
	topicsRoot string // CLAUDE_TOPICS_ROOT
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{
		t:          t,
		configDir:  filepath.Join(dir, "config"),
		topicsRoot: filepath.Join(dir, "config", "topics"),
	}
	mkdirAll(t, filepath.Join(e.configDir, "projects"))
	mkdirAll(t, e.topicsRoot)
	// Mark the profile as set up, or `topic` refuses to launch into it.
	writeJSON(t, filepath.Join(e.configDir, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
	})
	return e
}

type result struct {
	stdout, stderr string
	code           int
}

func (r result) out() string { return r.stdout + r.stderr }

// run invokes the CLI and returns its output and exit code without failing the test —
// exit codes are frequently the thing under assertion.
func (e *env) run(args ...string) result {
	e.t.Helper()
	cmd := exec.Command(topicBin(e.t), args...)
	cmd.Env = append(os.Environ(),
		"CLAUDE_CONFIG_DIR="+e.configDir,
		"CLAUDE_TOPICS_ROOT="+e.topicsRoot,
	)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("running topic %v: %v", args, err)
	}
	return result{stdout.String(), stderr.String(), code}
}

func (e *env) mustRun(args ...string) result {
	e.t.Helper()
	r := e.run(args...)
	if r.code != 0 {
		e.t.Fatalf("topic %v failed (exit %d)\n%s", args, r.code, r.out())
	}
	return r
}

// seedTopic writes a registry entry directly, so tests can set up state that would
// otherwise need a real session.
func (e *env) seedTopic(slug string, topic map[string]any) {
	e.t.Helper()
	dir := filepath.Join(e.topicsRoot, slug)
	mkdirAll(e.t, dir)
	writeJSON(e.t, filepath.Join(dir, "topic.json"), topic)
}

// seedTranscript writes a transcript for a session id, optionally with bridge-session
// records, mimicking the layout Claude Code uses.
func (e *env) seedTranscript(projectSlug, sessionID string, lines []string) string {
	e.t.Helper()
	dir := filepath.Join(e.configDir, "projects", projectSlug)
	mkdirAll(e.t, dir)
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *env) readTopic(slug string) map[string]any {
	e.t.Helper()
	var m map[string]any
	b, err := os.ReadFile(filepath.Join(e.topicsRoot, slug, "topic.json"))
	if err != nil {
		e.t.Fatalf("reading topic %s: %v", slug, err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		e.t.Fatalf("parsing topic %s: %v", slug, err)
	}
	return m
}

// newProfile creates a second, empty-but-set-up profile directory — a move
// destination. Returns its path, which `--to` accepts directly.
func (e *env) newProfile(name string) string {
	e.t.Helper()
	dir := filepath.Join(e.t.TempDir(), name)
	mkdirAll(e.t, filepath.Join(dir, "projects"))
	writeJSON(e.t, filepath.Join(dir, ".claude.json"), map[string]any{
		"hasCompletedOnboarding": true,
	})
	return dir
}

func (e *env) exists(parts ...string) bool {
	_, err := os.Stat(filepath.Join(append([]string{e.topicsRoot}, parts...)...))
	return err == nil
}

func mkdirAll(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
}

func mustContain(t *testing.T, got, want, context string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("%s: expected output to contain %q, got:\n%s", context, want, got)
	}
}
