package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A launch that stops at a startup question leaves its session running, and that
// session must be registered: the registry is the only thing tying the topic to the
// UUID, so an unregistered one is invisible to `topic down` and lost to the next `up`.

func stubLaunch(t *testing.T, err error) {
	t.Helper()
	orig := launch
	launch = func(configDir, name, dir string, claudeArgs ...string) error { return err }
	t.Cleanup(func() { launch = orig })
}

func parked() error {
	return fmt.Errorf("wrapped: %w", &leftRunningError{"waiting on a question"})
}

func TestRecordLaunch(t *testing.T) {
	recorded := false
	rec := func() error { recorded = true; return nil }

	if err := recordLaunch(nil, rec); err != nil || !recorded {
		t.Errorf("success: err=%v recorded=%v", err, recorded)
	}

	recorded = false
	if err := recordLaunch(parked(), rec); !isLeftRunning(err) || !recorded {
		t.Errorf("parked: err=%v recorded=%v — want the error returned AND the session recorded", err, recorded)
	}

	// Any other failure means no session survives: nothing to record.
	recorded = false
	boom := errors.New("claude did not start")
	if err := recordLaunch(boom, rec); err != boom || recorded {
		t.Errorf("failed launch: err=%v recorded=%v", err, recorded)
	}
}

func TestUpRecordsANewTopicParkedAtAPrompt(t *testing.T) {
	root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
	stubLaunch(t, parked())

	err := cmdUp([]string{"Parked", dir, cfg, root})
	if !isLeftRunning(err) {
		t.Fatalf("cmdUp = %v, want the parked error returned", err)
	}
	reg := load(topicFile(root, "Parked"))
	if scalar(reg["sessionId"]) == "" || scalar(reg["dir"]) != dir || scalar(reg["generation"]) != "1" {
		t.Errorf("session not recorded: %v", reg)
	}
}

func TestUpDoesNotRecordAFailedLaunch(t *testing.T) {
	root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
	stubLaunch(t, errors.New("claude did not start"))

	if err := cmdUp([]string{"Dead", dir, cfg, root}); err == nil {
		t.Fatal("cmdUp succeeded")
	}
	if _, err := os.Stat(topicFile(root, "Dead")); err == nil {
		t.Error("a launch that left no session still created a registry entry")
	}
}

func TestForkRecordsAChildParkedAtAPrompt(t *testing.T) {
	root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
	brief := filepath.Join(dir, "brief.md")
	os.WriteFile(brief, []byte("brief"), 0o600)
	stubLaunch(t, parked())

	err := cmdFork([]string{"Child", dir, "--brief", brief, cfg, root})
	if !isLeftRunning(err) {
		t.Fatalf("cmdFork = %v, want the parked error returned", err)
	}
	if reg := load(topicFile(root, "Child")); scalar(reg["sessionId"]) == "" || scalar(reg["seededFrom"]) != brief {
		t.Errorf("child not recorded: %v", reg)
	}
}

func TestHandoffSwapRecordsTheReplacementParkedAtAPrompt(t *testing.T) {
	root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
	doc := filepath.Join(dir, "handoff.md")
	os.WriteFile(doc, []byte("handoff"), 0o600)
	os.MkdirAll(filepath.Dir(topicFile(root, "Swap")), 0o755)
	save(topicFile(root, "Swap"), map[string]any{
		"name": "Swap", "dir": dir, "sessionId": "old-sid", "generation": "1", "state": "up"})
	stubLaunch(t, parked())

	err := cmdHandoffSwap([]string{"Swap", doc, cfg, root})
	if !isLeftRunning(err) {
		t.Fatalf("cmdHandoffSwap = %v, want the parked error returned", err)
	}
	reg := load(topicFile(root, "Swap"))
	if sid := scalar(reg["sessionId"]); sid == "" || sid == "old-sid" {
		t.Errorf("registry still names the retired session: %v", reg)
	}
	if scalar(reg["generation"]) != "2" {
		t.Errorf("generation = %v, want 2", reg["generation"])
	}
}

// claudeArgs are stored with the session, never before it: a launch that fails must
// not leave a registry entry holding nothing but claudeArgs (it would show up in
// `topic list` as a topic).
func TestFailedLaunchLeavesNoStubForUpOrFork(t *testing.T) {
	root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
	brief := filepath.Join(dir, "brief.md")
	os.WriteFile(brief, []byte("brief"), 0o600)
	stubLaunch(t, errors.New("claude did not start"))

	if err := cmdUp([]string{"Dead", dir, "--", "--verbose", cfg, root}); err == nil {
		t.Fatal("cmdUp succeeded")
	}
	if err := cmdFork([]string{"DeadChild", dir, "--brief", brief, "--", "--verbose", cfg, root}); err == nil {
		t.Fatal("cmdFork succeeded")
	}
	for _, n := range []string{"Dead", "DeadChild"} {
		if _, err := os.Stat(topicFile(root, n)); err == nil {
			t.Errorf("%s: a failed launch left a registry entry", n)
		}
	}
}

// ...but the args do reach the launch, and are stored once a session exists, whether
// it came up cleanly or is parked at a prompt.
func TestUpLaunchesWithAndStoresArgsOnceASessionExists(t *testing.T) {
	for name, launchErr := range map[string]error{"clean": nil, "parked": parked()} {
		root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
		var got []string
		orig := launch
		launch = func(_, _, _ string, a ...string) error { got = a; return launchErr }

		cmdUp([]string{"T", dir, "--", "--verbose", cfg, root})
		launch = orig

		if len(got) == 0 || got[0] != "--verbose" {
			t.Errorf("%s: launch args = %q, want --verbose first", name, got)
		}
		if stored := topicClaudeArgs(root, "T"); len(stored) != 1 || stored[0] != "--verbose" {
			t.Errorf("%s: stored claudeArgs = %q", name, stored)
		}
	}
}

// Args given for a topic that is already up are recorded only if it is registered.
func TestUpOnAnUnregisteredLiveTopicStoresNothing(t *testing.T) {
	root, cfg, dir := t.TempDir(), t.TempDir(), t.TempDir()
	tn := tmuxName(profileTag(cfg), "ZZ unit alive")
	// isUp needs a process named claude in the pane; a symlinked sleep is one.
	fake := filepath.Join(t.TempDir(), "claude")
	if sleep, err := exec.LookPath("sleep"); err != nil || os.Symlink(sleep, fake) != nil {
		t.Skip("cannot fake a claude process")
	}
	if tmuxRun("new-session", "-d", "-s", tn, fake+" 60") != nil {
		t.Skip("tmux unavailable")
	}
	defer killSession(tn)
	time.Sleep(500 * time.Millisecond)
	if !isUp(profileTag(cfg), "ZZ unit alive") {
		t.Skip("fake claude process not recognised as up")
	}
	stubLaunch(t, errors.New("must not launch: the topic is already up"))

	if err := cmdUp([]string{"ZZ unit alive", dir, "--", "--verbose", cfg, root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(topicFile(root, "ZZ unit alive")); err == nil {
		t.Error("created a registry entry holding only claudeArgs")
	}
}
