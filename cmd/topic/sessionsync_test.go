package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// /clear starts a new session id inside a running Claude, and topic only records an id
// when it launches one. Found 2026-10-02: a move carried the pre-/clear transcript to the
// other profile, stranded the live one, and the next `up` resumed the old conversation.

func writeJSON(t *testing.T, path string, m map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := save(path, m); err != nil {
		t.Fatal(err)
	}
}

func TestSessionIDOfPidsReadsTheLiveRecord(t *testing.T) {
	configDir := t.TempDir()
	me := os.Getpid()
	writeJSON(t, filepath.Join(configDir, "sessions", strconv.Itoa(me)+".json"),
		map[string]any{"pid": me, "sessionId": "after-clear"})

	if got := sessionIDOfPids(configDir, []int{me}); got != "after-clear" {
		t.Errorf("sessionIDOfPids = %q, want the id in the live record", got)
	}
}

func TestSessionIDOfPidsIgnoresStaleRecords(t *testing.T) {
	configDir := t.TempDir()
	me := os.Getpid()
	// A record whose pid field disagrees with its filename belongs to a recycled pid.
	writeJSON(t, filepath.Join(configDir, "sessions", strconv.Itoa(me)+".json"),
		map[string]any{"pid": me + 1, "sessionId": "someone-else"})
	if got := sessionIDOfPids(configDir, []int{me}); got != "" {
		t.Errorf("sessionIDOfPids trusted a record for another pid: %q", got)
	}
}

func withLiveSession(t *testing.T, sid string) {
	t.Helper()
	prev := liveSessionIDOf
	liveSessionIDOf = func(string, string) string { return sid }
	t.Cleanup(func() { liveSessionIDOf = prev })
}

func TestSyncSessionIDFollowsAClear(t *testing.T) {
	topicsRoot := t.TempDir()
	file := topicFile(topicsRoot, "T")
	writeJSON(t, file, map[string]any{"name": "T", "sessionId": "before-clear"})
	withLiveSession(t, "after-clear")

	if err := syncSessionID(t.TempDir(), topicsRoot, "T", false); err != nil {
		t.Fatal(err)
	}
	m := load(file)
	if got := scalar(m["sessionId"]); got != "after-clear" {
		t.Errorf("registry sessionId = %q, want the live session", got)
	}
	// The pre-/clear conversation must stay resumable, and travel with a move.
	if ids := sessionIDsOf(file); len(ids) != 2 || ids[1] != "before-clear" {
		t.Errorf("sessionIDsOf = %v, want the replaced session kept in history", ids)
	}
}

func TestSyncSessionIDDryRunChangesNothing(t *testing.T) {
	topicsRoot := t.TempDir()
	file := topicFile(topicsRoot, "T")
	writeJSON(t, file, map[string]any{"name": "T", "sessionId": "before-clear"})
	withLiveSession(t, "after-clear")

	if err := syncSessionID(t.TempDir(), topicsRoot, "T", true); err != nil {
		t.Fatal(err)
	}
	if got := scalar(load(file)["sessionId"]); got != "before-clear" {
		t.Errorf("dry run rewrote the registry: sessionId = %q", got)
	}
}

func TestSyncLiveSessionsFollowsOnlyTheDriftedTopic(t *testing.T) {
	topicsRoot := t.TempDir()
	drifted, steady := topicFile(topicsRoot, "Drifted"), topicFile(topicsRoot, "Steady")
	writeJSON(t, drifted, map[string]any{"name": "Drifted", "sessionId": "before-clear"})
	writeJSON(t, steady, map[string]any{"name": "Steady", "sessionId": "same"})
	prev := liveSessionIDOf
	liveSessionIDOf = func(_, name string) string {
		if name == "Drifted" {
			return "after-clear"
		}
		return "same"
	}
	t.Cleanup(func() { liveSessionIDOf = prev })

	syncLiveSessions(t.TempDir(), topicsRoot)

	if got := scalar(load(drifted)["sessionId"]); got != "after-clear" {
		t.Errorf("drifted topic sessionId = %q, want the live session", got)
	}
	if m := load(steady); scalar(m["sessionId"]) != "same" || m["history"] != nil {
		t.Errorf("steady topic was rewritten: %v", m)
	}
}

func TestSyncSessionIDLeavesADownTopicAlone(t *testing.T) {
	topicsRoot := t.TempDir()
	file := topicFile(topicsRoot, "T")
	writeJSON(t, file, map[string]any{"name": "T", "sessionId": "recorded"})
	withLiveSession(t, "")

	if err := syncSessionID(t.TempDir(), topicsRoot, "T", false); err != nil {
		t.Fatal(err)
	}
	m := load(file)
	if scalar(m["sessionId"]) != "recorded" || m["history"] != nil {
		t.Errorf("syncSessionID changed a topic with nothing running: %v", m)
	}
}
