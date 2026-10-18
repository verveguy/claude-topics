package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `topic ui --rows` is what the picker shows: topics grouped under their working
// directory, headings first. Hermetic — nothing here is a live session, so every
// topic reads as down.
func TestUIRowsGroupByDirectory(t *testing.T) {
	e := newEnv(t)
	e.seedTopic("zen", map[string]any{"name": "zen", "dir": "/work/jetpack"})
	e.seedTopic("orac", map[string]any{"name": "orac", "dir": "/work/jetpack"})
	e.seedTopic("kiln", map[string]any{"name": "Kiln", "dir": "/work/ainative"})

	got := e.mustRun("ui", "--rows").stdout
	want := "\t-- /work/ainative\n" +
		"down\tKiln\n" +
		"\t-- /work/jetpack\n" +
		"down\torac\n" +
		"down\tzen\n"
	if got != want {
		t.Errorf("rows =\n%q\nwant\n%q", got, want)
	}
}

// The layout must come up as two panes with the right one recorded, whatever the user's
// tmux.conf says. An early version addressed panes as "0.1" and silently built nothing
// under `base-index 1`, so this runs with exactly that config.
func TestUILayoutSurvivesBaseIndexOne(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	home := t.TempDir()
	conf := "set -g base-index 1\nsetw -g pane-base-index 1\n"
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := "topic-ui-test-" + filepath.Base(home)
	t.Setenv("HOME", home)
	t.Setenv("TOPIC_UI_SOCKET", sock)
	t.Setenv("TMUX", "") // as if launched from a bare terminal
	t.Cleanup(func() { _ = tmuxCommand("-L", sock, "kill-server").Run() })

	e := newEnv(t)
	e.mustRun("ui", "--no-attach")

	panes, err := tmuxCommand("-L", sock, "list-panes", "-F", "#{pane_id}").Output()
	if err != nil {
		t.Fatalf("listing panes: %v", err)
	}
	ids := strings.Fields(string(panes))
	if len(ids) != 2 {
		t.Fatalf("want 2 panes, got %v", ids)
	}
	right, err := tmuxCommand("-L", sock, "show", "-gv", "@right-pane").Output()
	if err != nil {
		t.Fatalf("@right-pane not set: %v", err)
	}
	if r := strings.TrimSpace(string(right)); r != ids[1] {
		t.Errorf("@right-pane = %q, want the second pane %q", r, ids[1])
	}

	// A second run must reuse the layout, not stack another one.
	e.mustRun("ui", "--no-attach")
	again, _ := tmuxCommand("-L", sock, "list-panes", "-F", "#{pane_id}").Output()
	if got := strings.Fields(string(again)); len(got) != 2 {
		t.Errorf("second run changed the layout: %v", got)
	}
}
