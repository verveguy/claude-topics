package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
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
