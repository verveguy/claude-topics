package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func seedRestoreTopic(t *testing.T, root, name string, fields ...string) {
	t.Helper()
	if err := cmdSet(append([]string{topicFile(root, name), "name", name}, fields...)); err != nil {
		t.Fatal(err)
	}
}

// What a crash leaves is "up" in the registry with nothing running. Everything else —
// a deliberate down, a session that exited by itself — stays down.
func TestRestoreCandidates(t *testing.T) {
	root := t.TempDir()
	seedRestoreTopic(t, root, "Crashed", "sessionId", "a", "state", "up")
	seedRestoreTopic(t, root, "Running", "sessionId", "b", "state", "up")
	seedRestoreTopic(t, root, "Exited", "sessionId", "c", "state", "exited")
	seedRestoreTopic(t, root, "Put down", "sessionId", "d", "state", "down")
	seedRestoreTopic(t, root, "Cycled", "sessionId", "e", "state", "down", restoreKey, "2026-11-07T10:00:00+0000")
	seedRestoreTopic(t, root, "Never launched", "state", "up") // no session to resume

	up := func(name string) bool { return name == "Running" }
	if got, want := restoreCandidates(root, nil, up), []string{"Crashed", "Cycled"}; !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
	if got, want := restoreCandidates(root, []string{"Cycled", "Exited"}, up), []string{"Cycled"}; !reflect.DeepEqual(got, want) {
		t.Errorf("candidates limited to names = %v, want %v", got, want)
	}
}

// The hook marks a session that ended by itself, and nothing else: `topic down` has
// already said down, and an adopted session is not topic's to change.
func TestSessionEndedMarksOnlyUpTopics(t *testing.T) {
	root, cfg := t.TempDir(), t.TempDir()
	seedRestoreTopic(t, root, "Quit", "state", "up")
	seedRestoreTopic(t, root, "Downed", "state", "down")
	seedRestoreTopic(t, root, "Adopted", "state", "adopted")
	for _, n := range []string{"Quit", "Downed", "Adopted", "Unregistered"} {
		if err := cmdSessionEnded([]string{n, cfg, root}); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
	}
	for name, want := range map[string]string{"Quit": "exited", "Downed": "down", "Adopted": "adopted"} {
		if got := regGet(root, name, "state"); got != want {
			t.Errorf("%s: state = %q, want %q", name, got, want)
		}
	}
	if fileExists(topicFile(root, "Unregistered")) {
		t.Error("the hook created a registry entry for a topic that had none")
	}
}

// The hook goes after claude on the same line, so it runs only once claude has exited.
func TestExitHookFollowsClaude(t *testing.T) {
	cfg := filepath.FromSlash("/p/.claude-work")
	hook := exitHook(cfg, "Fabrik work")
	if hook == "" {
		t.Skip("no topic binary next to the test binary")
	}
	want := " session-ended 'Fabrik work' " + shellQuote(cfg) + " " + shellQuote(filepath.Join(cfg, "topics"))
	if !strings.HasPrefix(hook, "; ") || !strings.HasSuffix(hook, want) {
		t.Errorf("hook = %q", hook)
	}
}
