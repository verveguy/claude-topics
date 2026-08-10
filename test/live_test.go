package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live tests really launch Claude Code. Session initialisation — trust prompts,
// first-run setup, Remote Control bridge minting — is where most of this tool's
// hard-won behaviour lives, and none of it can be faked convincingly.
//
// They need a real, logged-in profile: credentials are per-profile, so a throwaway
// config dir is NOT logged in (verified 2026-08-09). The registry is still sandboxed
// via CLAUDE_TOPICS_ROOT, so a live test cannot touch real topics — only the
// profile's projects/ gains a transcript, which the test removes.
//
//	TOPIC_TEST_PROFILE=~/.claude-personal go test ./test/
//
// Topics are launched with no seed prompt, so the session comes up idle and no
// inference is billed.

// liveEnv is an env whose config dir is a REAL profile, with a sandboxed registry.
func liveEnv(t *testing.T) *env {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping live test in -short mode")
	}
	profile := os.Getenv("TOPIC_TEST_PROFILE")
	if profile == "" {
		t.Skip("set TOPIC_TEST_PROFILE to a real logged-in profile to run live tests")
	}
	if strings.HasPrefix(profile, "~") {
		home, _ := os.UserHomeDir()
		profile = filepath.Join(home, strings.TrimPrefix(profile, "~"))
	}
	if _, err := os.Stat(filepath.Join(profile, ".claude.json")); err != nil {
		t.Fatalf("TOPIC_TEST_PROFILE=%s is not a Claude config dir: %v", profile, err)
	}
	return &env{
		t:          t,
		configDir:  profile,
		topicsRoot: filepath.Join(t.TempDir(), "topics"),
	}
}

// uniqueName keeps live topics obviously disposable and collision-free, since tmux
// session names are machine-global.
func uniqueName(t *testing.T) string {
	return fmt.Sprintf("ZZ Test %d %s", os.Getpid(), strings.NewReplacer("/", "-").Replace(t.Name()))
}

// tearDown stops the session and removes the transcript the test created.
func (e *env) tearDown(name, workDir string) {
	e.t.Helper()
	e.run("down", name)
	sid, _ := e.readTopicField(name, "sessionId")
	e.run("forget", name, "--purge")
	if sid != "" {
		matches, _ := filepath.Glob(filepath.Join(e.configDir, "projects", "*", sid+".jsonl"))
		for _, m := range matches {
			os.Remove(m)
		}
	}
	// Belt and braces: a half-started session leaves a tmux session behind.
	exec.Command("tmux", "kill-session", "-t", "="+tmuxName(e, name)).Run()
	os.RemoveAll(workDir)
}

func (e *env) readTopicField(name, field string) (string, bool) {
	e.t.Helper()
	r := e.run("status", name)
	for _, line := range strings.Split(r.out(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			return strings.TrimSpace(strings.TrimPrefix(line, field+":")), true
		}
	}
	return "", false
}

// tmuxName mirrors the CLI's own mapping: non-default profiles are namespaced.
func tmuxName(e *env, name string) string {
	home, _ := os.UserHomeDir()
	tag := ""
	if e.configDir != filepath.Join(home, ".claude") {
		tag = strings.TrimPrefix(strings.TrimPrefix(filepath.Base(e.configDir), "."), "claude-") + "/"
	}
	return tag + strings.NewReplacer(":", "-", ".", "-").Replace(name)
}

// A topic must start, be reported up, register a session, and put down losslessly —
// keeping its session id so the next `up` is a real --resume.
func TestLiveUpAndDownAreLossless(t *testing.T) {
	e := liveEnv(t)
	name := uniqueName(t)
	workDir := filepath.Join(t.TempDir(), "work")
	mkdirAll(t, workDir)
	t.Cleanup(func() { e.tearDown(name, workDir) })

	r := e.mustRun("up", name, workDir)
	mustContain(t, r.out(), "is up", "topic should report the session up")

	if !strings.Contains(e.mustRun("list").out(), name) {
		t.Fatal("topic does not appear in list after up")
	}
	sid, ok := e.readTopicField(name, "sessionId")
	if !ok || sid == "" {
		t.Fatal("no sessionId recorded after up")
	}

	e.mustRun("down", name)
	if after, _ := e.readTopicField(name, "sessionId"); after != sid {
		t.Errorf("sessionId changed across down: %s -> %s (put-down must be lossless)", sid, after)
	}
}

// Remote Control identity is minted at launch and recorded in the transcript.
// rebridge must cause a genuinely different bridge to be issued next time.
func TestLiveRebridgeMintsANewBridge(t *testing.T) {
	e := liveEnv(t)
	name := uniqueName(t)
	workDir := filepath.Join(t.TempDir(), "work")
	mkdirAll(t, workDir)
	t.Cleanup(func() { e.tearDown(name, workDir) })

	e.mustRun("up", name, workDir)
	sid, _ := e.readTopicField(name, "sessionId")
	first := waitForBridge(t, e, sid)

	e.mustRun("rebridge", name)
	second := waitForBridge(t, e, sid)

	if second == "" || second == first {
		t.Errorf("bridge did not change across rebridge: %q -> %q", first, second)
	}
}

// waitForBridge polls the transcript: the bridge record is written shortly after the
// session connects, not synchronously with launch.
func waitForBridge(t *testing.T, e *env, sid string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(e.configDir, "projects", "*", sid+".jsonl"))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			if id := lastBridgeID(string(b)); id != "" {
				return id
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

func lastBridgeID(body string) string {
	const key = `"bridgeSessionId":"`
	last := ""
	for i := 0; ; {
		j := strings.Index(body[i:], key)
		if j < 0 {
			break
		}
		start := i + j + len(key)
		end := strings.Index(body[start:], `"`)
		if end < 0 {
			break
		}
		last = body[start : start+end]
		i = start + end
	}
	return last
}
