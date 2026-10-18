package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `topic ui --rows` is what the picker shows: topics grouped under their working
// directory, headings first. Hermetic — nothing here is a live session, so every
// topic reads as down.
func TestUIRowsGroupByDirectory(t *testing.T) {
	t.Setenv("NO_COLOR", "1") // plain marks, so the rows compare as text
	e := newEnv(t)
	e.seedTopic("zen", map[string]any{"name": "zen", "dir": "/work/jetpack"})
	e.seedTopic("orac", map[string]any{"name": "orac", "dir": "/work/jetpack"})
	e.seedTopic("kiln", map[string]any{"name": "Kiln", "dir": "/work/ainative"})

	got := e.mustRun("ui", "--rows").stdout
	want := "\t── /work/ainative\t\n" +
		"down\t▼ Kiln\tKiln\n" +
		"\t── /work/jetpack\t\n" +
		"down\t▼ orac\torac\n" +
		"down\t▼ zen\tzen\n"
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

	// A client narrower than the window was built for must not squeeze the list: tmux
	// shrinks the first pane first, and a 54-column list once became 4.
	harness := sock + "-client"
	t.Cleanup(func() { _ = tmuxCommand("-L", harness, "kill-server").Run() })
	if err := tmuxCommand("-L", harness, "new-session", "-d", "-x", "120", "-y", "40",
		"env -u TMUX tmux -L "+sock+" attach -t "+sock).Run(); err != nil {
		t.Fatalf("attaching a client: %v", err)
	}
	var width string
	for i := 0; i < 50; i++ {
		out, _ := tmuxCommand("-L", sock, "list-panes", "-F", "#{pane_width}").Output()
		if f := strings.Fields(string(out)); len(f) == 2 {
			width = f[0]
			if width != "54" { // the build-time width: wait for the client's resize to land
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if w, _ := strconv.Atoi(width); w < 30 {
		t.Errorf("list pane is %q columns wide after a 120-column client attached; want >= 30", width)
	}

	// --quit closes the UI's server and nothing else.
	e.mustRun("ui", "--quit")
	if err := tmuxCommand("-L", sock, "has-session").Run(); err == nil {
		t.Error("the UI is still running after --quit")
	}
}
